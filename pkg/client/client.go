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
	Project  string      `json:"project"`
	Model    string      `json:"model"`
	Fallback bool        `json:"fallback"`
	TookMS   int64       `json:"took_ms"`
	Results  []SearchHit `json:"results"`
}

type Project struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	SourceType string `json:"source_type"`
	GitURL     string `json:"git_url,omitempty"`
	Branch     string `json:"branch,omitempty"`
}

type Job struct {
	ID            int    `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	FilesIndexed  int    `json:"files_indexed"`
	ChunksCreated int    `json:"chunks_created"`
}

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

// ---- Methods ------------------------------------------------------------------

// Healthz reports whether the server is reachable.
func (c *Client) Healthz(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil, nil)
}

// Search runs a semantic search over a project.
func (c *Client) Search(ctx context.Context, project, query, model string, topK int) (*SearchResponse, error) {
	body := map[string]any{"query": query, "top_k": topK, "model": model}
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

// GetJob fetches a job's status.
func (c *Client) GetJob(ctx context.Context, id int) (*Job, error) {
	var out Job
	if err := c.do(ctx, http.MethodGet, "/api/v1/jobs/"+strconv.Itoa(id), nil, &out); err != nil {
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

// FilesBatch uploads file contents to index and removes the delete list.
func (c *Client) FilesBatch(ctx context.Context, project string, files []BatchFile, del []string) (*BatchResponse, error) {
	var out BatchResponse
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/files/batch",
		map[string]any{"files": files, "delete": del}, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
