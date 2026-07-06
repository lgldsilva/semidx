// Package server exposes semidx over HTTP: the central API a many-machine
// deployment talks to, so clients never touch the database directly. This first
// slice serves health/readiness/metrics and an authenticated search endpoint;
// project/indexing/git-sync endpoints are added in later increments.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/webadmin"
	"github.com/lgldsilva/semidx/pkg/client"
)

// headerContentType is the HTTP Content-Type header name.
const headerContentType = "Content-Type"

// Server is the HTTP API. It owns the store, embedder and search service; token
// auth is enforced per route.
type Server struct {
	store           store.Store
	emb             embed.Embedder
	search          *search.Service
	log             *slog.Logger
	reg             *prometheus.Registry
	reqs            *prometheus.CounterVec
	searchDuration  *prometheus.HistogramVec
	requestDuration *prometheus.HistogramVec
	activeRequests  *prometheus.GaugeVec
	admin           http.Handler    // the /admin management UI, nil unless MountAdmin was called
	jwt             *jwtauth.Issuer // JWT control-token verifier, nil unless EnableJWT was called

	apiLimiter *apiRateLimiter // per-key API rate limiter
}

// EnableJWT turns on JWT control tokens using secret as the HS256 signing key.
// With it set, the API accepts JWT bearers and the web UI can mint them.
func (s *Server) EnableJWT(secret string) error {
	iss, err := jwtauth.New(secret)
	if err != nil {
		return err
	}
	s.jwt = iss
	return nil
}

// MountAdmin enables the web management UI at /admin. secureCookies must be true
// when the server is reached over HTTPS (directly or via a TLS proxy). If JWT is
// enabled (EnableJWT), the UI can also mint control tokens.
func (s *Server) MountAdmin(secureCookies bool, csrfKey string) error {
	a, err := webadmin.New(s.store, s.emb, s.log, secureCookies, s.jwt, csrfKey)
	if err != nil {
		return err
	}
	s.admin = a.Handler()
	return nil
}

// New builds a Server. A nil logger falls back to slog.Default().
func New(st store.Store, emb embed.Embedder, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	reg := prometheus.NewRegistry()
	reqs := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "semidx_http_requests_total",
		Help: "Total HTTP requests by method and status.",
	}, []string{"method", "status"})
	reg.MustRegister(reqs)

	searchDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "semidx_search_duration_seconds",
		Help:    "Search request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"project"})
	reg.MustRegister(searchDuration)

	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "semidx_http_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
	reg.MustRegister(requestDuration)

	activeRequests := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "semidx_http_requests_in_flight",
		Help: "Number of HTTP requests currently being processed.",
	}, []string{"method"})
	reg.MustRegister(activeRequests)

	return &Server{store: st, emb: emb, search: search.NewService(st, emb), log: log, reg: reg, reqs: reqs, searchDuration: searchDuration, requestDuration: requestDuration, activeRequests: activeRequests, apiLimiter: newAPIRateLimiter()}
}

// Handler returns the fully-wired HTTP handler (routing + metrics instrumentation).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))

	mux.Handle("POST /api/v1/projects", s.limited(1<<20, s.authed("write", s.handleCreateProject)))
	mux.Handle("GET /api/v1/projects", s.authed("read", s.handleListProjects))
	mux.Handle("GET /api/v1/projects/{project}", s.authed("read", s.handleGetProject))
	mux.Handle("DELETE /api/v1/projects/{project}", s.authed("write", s.handleDeleteProject))
	mux.Handle("POST /api/v1/projects/{project}/search", s.limited(1<<20, s.authed("read", s.handleSearch)))
	mux.Handle("POST /api/v1/projects/{project}/index-jobs", s.limited(100<<10, s.authed("write", s.handleEnqueueJob)))
	mux.Handle("POST /api/v1/projects/{project}/files/diff", s.limited(10<<20, s.authed("write", s.handleFilesDiff)))
	mux.Handle("POST /api/v1/projects/{project}/files/batch", s.limited(100<<20, s.authed("write", s.handleFilesBatch)))
	mux.Handle("GET /api/v1/jobs/{id}", s.authed("read", s.handleGetJob))
	if s.admin != nil {
		mux.Handle("/admin/", s.admin)
	}
	return s.rateLimit(s.instrument(mux))
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	// #nosec G118 -- shutdown must use a fresh context; the request/serve context is already cancelled here.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	s.log.Info("http server listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(headerContentType, "text/plain")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not ready")
		return
	}
	w.Header().Set(headerContentType, "text/plain")
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	var body struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Query == "" {
		writeJSONError(w, http.StatusBadRequest, "query is required")
		return
	}

	start := time.Now()
	resp, err := s.search.Search(r.Context(), search.Request{
		Project: project, Query: body.Query, Model: body.Model, TopK: body.TopK,
	})
	if err != nil {
		s.log.Warn("search failed", "project", project, "err", err)
		var re interface{ RetryAfter() time.Duration }
		if errors.As(err, &re) {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(re.RetryAfter().Seconds()))))
			writeJSONError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}

	out := client.SearchResponse{
		Project:  project,
		Model:    resp.Model,
		Fallback: resp.Fallback,
		TookMS:   time.Since(start).Milliseconds(),
		Results:  make([]client.SearchHit, 0, len(resp.Results)),
	}
	for _, hit := range resp.Results {
		out.Results = append(out.Results, client.SearchHit{
			Path: hit.FilePath, StartLine: hit.StartLine, EndLine: hit.EndLine,
			Score: hit.Score, Content: hit.Content,
		})
	}
	s.searchDuration.WithLabelValues(project).Observe(time.Since(start).Seconds())
	writeJSON(w, http.StatusOK, out)
}

// instrument wraps the mux to count requests by method and status, track
// in-flight count, and record request duration.
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.activeRequests.WithLabelValues(r.Method).Inc()
		defer s.activeRequests.WithLabelValues(r.Method).Dec()

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.reqs.WithLabelValues(r.Method, http.StatusText(rec.status)).Inc()
		s.requestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(time.Since(start).Seconds())
	})
}

// limited wraps a handler so that the request body is capped at maxBytes.
// Excess bytes trigger an early 413 without reading further.
func (s *Server) limited(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

// rateLimit rejects requests that exceed 200 req/s per key (bearer token or IP).
// Health/ready/metrics/admin paths are excluded.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/healthz" || path == "/readyz" || path == "/metrics" || strings.HasPrefix(path, "/admin") {
			next.ServeHTTP(w, r)
			return
		}
		key := r.RemoteAddr
		if tok := r.Header.Get("Authorization"); tok != "" {
			key = tok
		}
		if !s.apiLimiter.allow(key) {
			writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// apiRateLimiter is a per-key token-bucket rate limiter (200 req/s per key).
type apiRateLimiter struct {
	mu     sync.Mutex
	counts map[string]*rateBucket
}

type rateBucket struct {
	count  int
	window time.Time
}

func newAPIRateLimiter() *apiRateLimiter {
	l := &apiRateLimiter{counts: map[string]*rateBucket{}}
	go l.reap()
	return l
}

func (l *apiRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.counts[key]
	if !ok || now.After(b.window) {
		l.counts[key] = &rateBucket{count: 1, window: now.Add(time.Second)}
		return true
	}
	if b.count >= 200 {
		return false
	}
	b.count++
	return true
}

func (l *apiRateLimiter) reap() {
	for {
		time.Sleep(5 * time.Minute)
		l.mu.Lock()
		now := time.Now()
		for k, b := range l.counts {
			if now.After(b.window) {
				delete(l.counts, k)
			}
		}
		l.mu.Unlock()
	}
}
