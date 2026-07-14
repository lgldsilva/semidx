// Package server exposes semidx over HTTP: the central API a many-machine
// deployment talks to, so clients never touch the database directly. This first
// slice serves health/readiness/metrics and an authenticated search endpoint;
// project/indexing/git-sync endpoints are added in later increments.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/secretbox"
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
	jobsQueued      prometheus.Gauge
	jobsRunning     prometheus.Gauge
	jobsTotal       *prometheus.CounterVec
	dbPoolTotal     prometheus.Gauge
	dbPoolIdle      prometheus.Gauge
	dbPoolAcquired  prometheus.Gauge
	dbPoolMax       prometheus.Gauge
	embedDuration   *prometheus.HistogramVec
	embedInputs     *prometheus.CounterVec
	admin           http.Handler    // the /admin management UI, nil unless MountAdmin was called
	mcpHTTP         http.Handler    // the /mcp Streamable HTTP endpoint, nil unless EnableMCPHTTP was called
	jwt             *jwtauth.Issuer // JWT control-token verifier, nil unless EnableJWT was called

	apiLimiter     *apiRateLimiter
	gitAllowFile   bool   // allow file:// git URLs (SEMIDX_GIT_ALLOW_FILE)
	gitSSLNoVerify bool   // disable TLS verify on git clone/pull (SEMIDX_GIT_SSL_NO_VERIFY)
	gitToken       string // token for private HTTPS clones (SEMIDX_GIT_TOKEN)
	gitUser        string // basic-auth user for gitToken (SEMIDX_GIT_USER)
	metricsToken   string // when set, /metrics requires Bearer match (SEMIDX_METRICS_TOKEN)
	indexLimits    IndexLimits
	secrets        *secretbox.Box // decrypts stored git credentials, nil-safe (SetSecretBox)
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
// enabled (EnableJWT), the UI can also mint control tokens. The returned Admin
// can be further configured (e.g. SetChat) before the server starts serving.
func (s *Server) MountAdmin(secureCookies bool, csrfKey string) (*webadmin.Admin, error) {
	a, err := webadmin.New(s.store, s.emb, s.log, secureCookies, s.jwt, csrfKey)
	if err != nil {
		return nil, err
	}
	s.admin = a.Handler()
	return a, nil
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

	jobsQueued := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "semidx_index_jobs_queued_estimated",
		Help: "Estimated number of queued index jobs (best-effort in-process counter).",
	})
	reg.MustRegister(jobsQueued)

	jobsRunning := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "semidx_index_jobs_running",
		Help: "Number of index jobs currently running in this server process.",
	})
	reg.MustRegister(jobsRunning)

	jobsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "semidx_index_jobs_total",
		Help: "Total index jobs by type and terminal status.",
	}, []string{"type", "status"})
	reg.MustRegister(jobsTotal)

	dbPoolTotal := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "semidx_db_pool_total_connections",
		Help: "Total PostgreSQL pool connections.",
	})
	dbPoolIdle := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "semidx_db_pool_idle_connections",
		Help: "Idle PostgreSQL pool connections.",
	})
	dbPoolAcquired := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "semidx_db_pool_acquired_connections",
		Help: "Acquired (in-use) PostgreSQL pool connections.",
	})
	dbPoolMax := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "semidx_db_pool_max_connections",
		Help: "Configured max PostgreSQL pool connections.",
	})
	reg.MustRegister(dbPoolTotal, dbPoolIdle, dbPoolAcquired, dbPoolMax)

	// REQ-OPS-03: embedding-call observability. The embed package stays
	// metrics-library-agnostic; the server wraps the embedder with an observer
	// that feeds these Prometheus series (model × ok/error), so every server
	// embed call — search query embedding included — is timed and counted.
	embedDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "semidx_embed_duration_seconds",
		Help:    "Embedding provider call duration in seconds by model and outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"model", "outcome"})
	embedInputs := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "semidx_embed_inputs_total",
		Help: "Total number of texts submitted to the embedder by model.",
	}, []string{"model"})
	reg.MustRegister(embedDuration, embedInputs)
	emb = embed.Instrument(emb, &embedObserver{dur: embedDuration, inputs: embedInputs})

	svc := search.NewService(st, emb)
	// Optional top-K reranker (REQ-SRCH-11), off unless SEMIDX_RERANK_WEIGHT is a
	// value in (0,1]. Kept env-gated so the default search path is unchanged.
	if r := rerankerFromEnv(log); r != nil {
		svc.SetReranker(r)
	}

	return &Server{
		store:           st,
		emb:             emb,
		search:          svc,
		log:             log,
		reg:             reg,
		reqs:            reqs,
		searchDuration:  searchDuration,
		requestDuration: requestDuration,
		activeRequests:  activeRequests,
		jobsQueued:      jobsQueued,
		jobsRunning:     jobsRunning,
		jobsTotal:       jobsTotal,
		dbPoolTotal:     dbPoolTotal,
		dbPoolIdle:      dbPoolIdle,
		dbPoolAcquired:  dbPoolAcquired,
		dbPoolMax:       dbPoolMax,
		embedDuration:   embedDuration,
		embedInputs:     embedInputs,
		apiLimiter:      newAPIRateLimiter(),
	}
}

// embedObserver adapts embed.Observer to the server's Prometheus series.
type embedObserver struct {
	dur    *prometheus.HistogramVec
	inputs *prometheus.CounterVec
}

func (o *embedObserver) ObserveEmbed(model string, inputs int, d time.Duration, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	o.dur.WithLabelValues(model, outcome).Observe(d.Seconds())
	o.inputs.WithLabelValues(model).Add(float64(inputs))
}

// rerankerFromEnv returns a LexicalReranker when SEMIDX_RERANK_WEIGHT parses to
// a value in (0,1], else nil (reranking disabled). An unparseable or
// out-of-range value logs a warning and disables reranking rather than guessing.
func rerankerFromEnv(log *slog.Logger) search.Reranker {
	raw := strings.TrimSpace(os.Getenv("SEMIDX_RERANK_WEIGHT"))
	if raw == "" {
		return nil
	}
	w, err := strconv.ParseFloat(raw, 64)
	if err != nil || w <= 0 || w > 1 {
		log.Warn("ignoring SEMIDX_RERANK_WEIGHT (want a float in (0,1])", "value", raw)
		return nil
	}
	log.Info("top-K reranker enabled", "weight", w)
	return search.NewLexicalReranker(w)
}

// SetGitAllowFile enables file:// git URLs for server-side git sync.
func (s *Server) SetGitAllowFile(v bool) { s.gitAllowFile = v }

// SetGitAuth configures TLS handling and credentials for server-side git sync:
// sslNoVerify accepts self-signed hosts; token authenticates private HTTPS
// clones (user defaults to x-access-token).
func (s *Server) SetGitAuth(sslNoVerify bool, token, user string) {
	s.gitSSLNoVerify = sslNoVerify
	s.gitToken = token
	s.gitUser = user
}

// SetMetricsToken requires a matching Bearer token on GET /metrics when non-empty.
func (s *Server) SetMetricsToken(token string) { s.metricsToken = token }

func (s *Server) metricsHandler() http.Handler {
	h := promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{})
	if s.metricsToken == "" {
		return h
	}
	want := s.metricsToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := bearerToken(r)
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeJSONError(w, http.StatusUnauthorized, "metrics token required")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// Handler returns the fully-wired HTTP handler (routing + metrics instrumentation).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.Handle("GET /metrics", s.metricsHandler())

	mux.Handle("POST /api/v1/projects", s.limited(1<<20, s.authed("write", s.handleCreateProject)))
	mux.Handle("GET /api/v1/projects", s.authed("read", s.handleListProjects))
	mux.Handle("GET /api/v1/projects/{project}", s.authed("read", s.handleGetProject))
	mux.Handle("DELETE /api/v1/projects/{project}", s.authed("write", s.handleDeleteProject))
	mux.Handle("GET /api/v1/projects/{project}/status", s.authed("read", s.handleProjectStatus))
	mux.Handle("POST /api/v1/projects/{project}/search", s.limited(1<<20, s.authed("read", s.handleSearch)))
	mux.Handle("POST /api/v1/projects/{project}/index-jobs", s.limited(100<<10, s.authed("write", s.handleEnqueueJob)))
	mux.Handle("POST /api/v1/projects/{project}/files/diff", s.limited(10<<20, s.authed("write", s.handleFilesDiff)))
	mux.Handle("POST /api/v1/projects/{project}/files/batch", s.limited(100<<20, s.authed("write", s.handleFilesBatch)))
	mux.Handle("GET /api/v1/projects/{project}/jobs/{id}", s.authed("read", s.handleGetProjectJob))
	mux.Handle("GET /api/v1/jobs/{id}", s.authed("read", s.handleGetJob))
	if s.mcpHTTP != nil {
		// MCP over Streamable HTTP (opt-in via EnableMCPHTTP). Same bearer auth
		// as the REST API; the body cap matches the search endpoint's.
		mux.Handle("/mcp", s.limited(1<<20, s.authed("read", s.mcpHTTP.ServeHTTP)))
	}
	if s.admin != nil {
		mux.Handle("/admin/", s.admin)
	}
	return s.rateLimit(s.instrument(mux))
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context, addr string) error {
	// WriteTimeout is 120s (not 30s) so SSE admin chat streams can complete;
	// webchat uses the same budget. ReadTimeout still caps request body reads.
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
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
		Query      string `json:"query"`
		TopK       int    `json:"top_k"`
		Model      string `json:"model"`
		Keyword    bool   `json:"keyword"`
		Graph      bool   `json:"graph"`
		GraphDepth int    `json:"graph_depth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Query == "" {
		writeJSONError(w, http.StatusBadRequest, "query is required")
		return
	}
	if body.GraphDepth <= 0 {
		body.GraphDepth = search.DefaultGraphDepth
	}
	body.GraphDepth = search.ClampGraphDepth(body.GraphDepth)

	start := time.Now()
	resp, err := s.search.Search(r.Context(), search.Request{
		Project: project, Query: body.Query, Model: body.Model, TopK: body.TopK,
		KeywordOnly: body.Keyword, Graph: body.Graph, GraphMaxDepth: body.GraphDepth,
	})
	if err != nil {
		s.log.Warn("search failed", "project", project, "err", err)
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "project not found")
			return
		}
		// An open embedding circuit no longer reaches here: the search service
		// degrades to keyword results (Degraded + RetryAfterMS in the response)
		// instead of propagating the RetryableError.
		writeJSONError(w, http.StatusInternalServerError, "search failed")
		return
	}

	out := client.SearchResponse{
		Project:      resp.Project.Name,
		Model:        resp.Model,
		Fallback:     resp.Fallback,
		Degraded:     resp.Degraded,
		RetryAfterMS: resp.RetryAfter.Milliseconds(),
		TookMS:       time.Since(start).Milliseconds(),
		Results:      make([]client.SearchHit, 0, len(resp.Results)),
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
		s.observeDBPool()
		s.activeRequests.WithLabelValues(r.Method).Inc()
		defer s.activeRequests.WithLabelValues(r.Method).Dec()

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.reqs.WithLabelValues(r.Method, http.StatusText(rec.status)).Inc()
		s.requestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(time.Since(start).Seconds())
	})
}

func (s *Server) observeDBPool() {
	type poolStatser interface {
		PoolStats() store.DBPoolStats
	}
	ps, ok := s.store.(poolStatser)
	if !ok {
		return
	}
	st := ps.PoolStats()
	s.dbPoolTotal.Set(float64(st.TotalConns))
	s.dbPoolIdle.Set(float64(st.IdleConns))
	s.dbPoolAcquired.Set(float64(st.AcquiredConns))
	s.dbPoolMax.Set(float64(st.MaxConns))
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

// Flush forwards to the underlying ResponseWriter when it supports streaming
// (SSE chat). Embedding http.ResponseWriter does not promote Flush, so without
// this method instrument() breaks w.(http.Flusher) for every /admin request.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer for http.ResponseController and similar.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
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
