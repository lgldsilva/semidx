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
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// Server is the HTTP API. It owns the store, embedder and search service; token
// auth is enforced per route.
type Server struct {
	store  store.Store
	emb    embed.Embedder
	search *search.Service
	log    *slog.Logger
	reg    *prometheus.Registry
	reqs   *prometheus.CounterVec
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
	return &Server{store: st, emb: emb, search: search.NewService(st, emb), log: log, reg: reg, reqs: reqs}
}

// Handler returns the fully-wired HTTP handler (routing + metrics instrumentation).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))

	mux.Handle("POST /api/v1/projects", s.authed("write", s.handleCreateProject))
	mux.Handle("GET /api/v1/projects", s.authed("read", s.handleListProjects))
	mux.Handle("GET /api/v1/projects/{project}", s.authed("read", s.handleGetProject))
	mux.Handle("DELETE /api/v1/projects/{project}", s.authed("write", s.handleDeleteProject))
	mux.Handle("POST /api/v1/projects/{project}/search", s.authed("read", s.handleSearch))
	mux.Handle("POST /api/v1/projects/{project}/index-jobs", s.authed("write", s.handleEnqueueJob))
	mux.Handle("GET /api/v1/jobs/{id}", s.authed("read", s.handleGetJob))
	return s.instrument(mux)
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
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
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "database not ready")
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ready"))
}

type searchHit struct {
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Content   string  `json:"content"`
}

type searchResponse struct {
	Project  string      `json:"project"`
	Model    string      `json:"model"`
	Fallback bool        `json:"fallback"`
	TookMS   int64       `json:"took_ms"`
	Results  []searchHit `json:"results"`
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
		// The only user-facing error today is a missing project.
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	out := searchResponse{
		Project:  project,
		Model:    resp.Model,
		Fallback: resp.Fallback,
		TookMS:   time.Since(start).Milliseconds(),
		Results:  make([]searchHit, 0, len(resp.Results)),
	}
	for _, hit := range resp.Results {
		out.Results = append(out.Results, searchHit{
			Path: hit.FilePath, StartLine: hit.StartLine, EndLine: hit.EndLine,
			Score: hit.Score, Content: hit.Content,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// instrument wraps the mux to count requests by method and status.
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.reqs.WithLabelValues(r.Method, http.StatusText(rec.status)).Inc()
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
