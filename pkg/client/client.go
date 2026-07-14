// Package client is a Go SDK for the semidx HTTP API. It is the public surface
// third parties (and the semidx CLI in remote mode) use to talk to a server, so
// it defines its own DTOs and depends on nothing internal.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// projectsPath is the base path for per-project API routes.
const projectsPath = "/api/v1/projects/"

// Client talks to a semidx server with a Bearer token.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient overrides the default *http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New returns a client for baseURL authenticating with token.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is a non-2xx response from the server.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("semidx: unexpected status %d", e.Status)
	}
	return fmt.Sprintf("semidx: %d: %s", e.Status, e.Message)
}

// ---- DTOs (mirror the server's JSON) ------------------------------------------

type SearchHit struct {
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Content   string  `json:"content"`
}

type SearchResponse struct {
	Project  string `json:"project"`
	Model    string `json:"model"`
	Fallback bool   `json:"fallback"`
	// Degraded is true when the embedding circuit was open on the server and
	// keyword results were served; RetryAfterMS hints when to retry semantic
	// search. Absent (false/0) on older servers.
	Degraded     bool        `json:"degraded"`
	RetryAfterMS int64       `json:"retry_after_ms"`
	TookMS       int64       `json:"took_ms"`
	Results      []SearchHit `json:"results"`
}

type Project struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	SourceType string `json:"source_type"`
	GitURL     string `json:"git_url,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Identity   string `json:"identity,omitempty"`
	Path       string `json:"path,omitempty"`
}

type Job struct {
	ID            int    `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	ProgressDone  int    `json:"progress_done,omitempty"`
	ProgressTotal int    `json:"progress_total,omitempty"`
	// ProgressPercent is optional and may be omitted by older servers.
	ProgressPercent int `json:"progress_percent,omitempty"`
	FilesIndexed    int `json:"files_indexed"`
	ChunksCreated   int `json:"chunks_created"`
	DeletedFiles    int `json:"deleted_files"`
	ErrorCount      int `json:"error_count"`
}

// Job status values returned by the server.
const (
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
	JobStatusRunning   = "running"
	JobStatusQueued    = "queued"
)

type DiffResponse struct {
	Stale   []string `json:"stale"`
	Deleted []string `json:"deleted"`
}

// EmbeddedChunk carries a pre-computed embedding for one chunk of a file. When a
// BatchFile includes Chunks, the server stores the embeddings directly instead of
// running its own chunking + embedding pipeline.
type EmbeddedChunk struct {
	StartLine int       `json:"start_line"`
	EndLine   int       `json:"end_line"`
	Content   string    `json:"content"`
	Embedding []float32 `json:"embedding"`
}

type BatchFile struct {
	Path    string          `json:"path"`
	Content string          `json:"content,omitempty"`
	Chunks  []EmbeddedChunk `json:"chunks,omitempty"`
}

type BatchResponse struct {
	Indexed int `json:"indexed"`
	Chunks  int `json:"chunks"`
	Deleted int `json:"deleted"`
	Errors  int `json:"errors"`
}

type EnqueueResponse struct {
	JobID  int    `json:"job_id"`
	Status string `json:"status"`
}

type StatusResponse struct {
	Name       string `json:"name"`
	Identity   string `json:"identity,omitempty"`
	SourceType string `json:"source_type"`
	Status     string `json:"status"`
	Model      string `json:"model"`
	TotalFiles int    `json:"total_files"`
}

// ---- Methods ------------------------------------------------------------------

// Healthz reports whether the server is reachable.
func (c *Client) Healthz(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil, nil)
}

// SearchParams carries the optional knobs for Client.Search. Grouping them in a
// struct keeps Search under the parameter-count limit (S107) and lets callers
// set only what they need (zero values mean: default model/top-k, semantic
// search, no graph expansion).
type SearchParams struct {
	Model      string // optional model override; "" uses the project's stored model
	TopK       int    // <= 0 lets the server default it
	Keyword    bool   // keyword-only search; never contacts the embedding provider
	Graph      bool   // expand results via the dependency graph (Graph-RAG)
	GraphDepth int    // max BFS depth for graph expansion
}

// Search runs a search over a project. With Params.Keyword the server searches
// by keyword only and never contacts the embedding provider (the remote-mode
// equivalent of the CLI's --keyword); otherwise it runs a semantic search that
// transparently falls back to keyword when embeddings are unavailable.
func (c *Client) Search(ctx context.Context, project, query string, p SearchParams) (*SearchResponse, error) {
	body := map[string]any{"query": query, "top_k": p.TopK, "model": p.Model, "keyword": p.Keyword, "graph": p.Graph, "graph_depth": p.GraphDepth}
	var out SearchResponse
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/search", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListProjects returns all registered projects.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out struct {
		Projects []Project `json:"projects"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/projects", nil, &out); err != nil {
		return nil, err
	}
	return out.Projects, nil
}

// GetProject fetches one project.
func (c *Client) GetProject(ctx context.Context, name string) (*Project, error) {
	var out Project
	if err := c.do(ctx, http.MethodGet, projectsPath+esc(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateProject registers a project. sourceType is "push" or "git".
func (c *Client) CreateProject(ctx context.Context, name, model, sourceType, gitURL, branch string) (*Project, error) {
	body := map[string]any{
		"name":  name,
		"model": model,
		"source": map[string]string{
			"type": sourceType, "url": gitURL, "branch": branch,
		},
	}
	var out Project
	if err := c.do(ctx, http.MethodPost, "/api/v1/projects", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Status returns the indexing status and file count for a project.
func (c *Client) Status(ctx context.Context, project string) (*StatusResponse, error) {
	var out StatusResponse
	if err := c.do(ctx, http.MethodGet, projectsPath+esc(project)+"/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProject removes a project.
func (c *Client) DeleteProject(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, projectsPath+esc(name), nil, nil)
}

// EnqueueJob queues an index job and returns its id.
func (c *Client) EnqueueJob(ctx context.Context, project, jobType string) (int, error) {
	var out struct {
		JobID int `json:"job_id"`
	}
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/index-jobs",
		map[string]string{"type": jobType}, &out); err != nil {
		return 0, err
	}
	return out.JobID, nil
}

// GetJob fetches a job's status scoped to a project (prevents cross-project IDOR).
func (c *Client) GetJob(ctx context.Context, project string, id int) (*Job, error) {
	var out Job
	path := projectsPath + esc(project) + "/jobs/" + strconv.Itoa(id)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FilesDiff reports which of the given path→hash files are stale or deleted.
func (c *Client) FilesDiff(ctx context.Context, project string, hashes map[string]string) (*DiffResponse, error) {
	var out DiffResponse
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/files/diff",
		map[string]any{"files": hashes}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FilesBatch uploads file contents to index and removes the delete list,
// using the synchronous mode (?sync=true) so the response includes the
// indexing counts directly.
func (c *Client) FilesBatch(ctx context.Context, project string, files []BatchFile, del []string) (*BatchResponse, error) {
	var out BatchResponse
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/files/batch?sync=true",
		map[string]any{"files": files, "delete": del}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FilesBatchAsync enqueues a batch indexing job and returns the job id. The
// job is processed by a background worker; call WaitForJob to poll for completion.
// Requires server support for async batches (status 202 Accepted).
func (c *Client) FilesBatchAsync(ctx context.Context, project string, files []BatchFile, del []string) (int, error) {
	body := map[string]any{"files": files, "delete": del}
	resp, err := c.doResponse(ctx, http.MethodPost, projectsPath+esc(project)+"/files/batch", body)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		// Server returned sync response (old server or --sync equivalent) —
		// this is not an async batch. Don't silently return job_id=0.
		return 0, fmt.Errorf("server does not support async batch (status %d) — use --sync", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusAccepted {
		return 0, &APIError{Status: resp.StatusCode, Message: "unexpected status for async batch"}
	}

	var out EnqueueResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	if out.JobID == 0 {
		return 0, fmt.Errorf("server returned empty job_id")
	}
	return out.JobID, nil
}

// WaitForJob polls a job until it completes, fails, or ctx is cancelled.
// interval controls the polling frequency. Returns the final job state.
func (c *Client) WaitForJob(ctx context.Context, project string, jobID int, interval time.Duration) (*Job, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		job, err := c.GetJob(ctx, project, jobID)
		if err != nil {
			return nil, err
		}
		if job.Status == JobStatusSucceeded || job.Status == JobStatusFailed {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-ticker.C:
		}
	}
}

// esc escapes a path segment for use in REST URLs. url.PathEscape does not
// encode '/', so names containing a slash would be interpreted as multiple
// path segments; we replace any remaining slash with %2F.
func esc(s string) string { return strings.ReplaceAll(url.PathEscape(s), "/", "%2F") }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// doResponse is like do but returns the raw *http.Response so callers can
// inspect the status code before decoding the body. The caller must close
// resp.Body.
func (c *Client) doResponse(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.http.Do(req)
}
