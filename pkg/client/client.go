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
	baseURL   string
	token     string
	tenant    string
	workspace string
	http      *http.Client
	// ClientSource is sent as X-Semidx-Client (cli|mcp|sdk|admin) so the server
	// can attribute search usage analytics. Empty → header omitted.
	ClientSource string
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient overrides the default *http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithTenant selects the tenant slug sent with each request. The server still
// verifies membership; this header is a selector, never an authority.
func WithTenant(slug string) Option { return func(c *Client) { c.tenant = strings.TrimSpace(slug) } }

// WithWorkspace selects the workspace slug sent with each request. The server
// verifies that it belongs to the selected tenant before applying the scope.
func WithWorkspace(slug string) Option {
	return func(c *Client) { c.workspace = strings.TrimSpace(slug) }
}

// WithClientSource sets the X-Semidx-Client header value (cli|mcp|sdk|admin).
func WithClientSource(src string) Option {
	return func(c *Client) { c.ClientSource = src }
}

// HeaderClientSource is the HTTP header remote clients send so the server can
// attribute search usage to cli vs mcp vs sdk vs admin.
const HeaderClientSource = "X-Semidx-Client"

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
	Project     string  `json:"project,omitempty"`
	Path        string  `json:"path"`
	StartLine   int     `json:"start_line"`
	EndLine     int     `json:"end_line"`
	Score       float64 `json:"score"`
	FusionScore float64 `json:"fusion_score,omitempty"`
	SourceRank  int     `json:"source_rank,omitempty"`
	Content     string  `json:"content"`
	Confidence  string  `json:"confidence,omitempty"`
	Symbol      string  `json:"symbol,omitempty"`
	// Stale is true when the file changed since indexing (server-side check).
	Stale bool `json:"stale,omitempty"`
	// IndexedAt is when the file version was last indexed (RFC3339; empty if unknown).
	IndexedAt time.Time `json:"indexed_at,omitempty"`
}

type SearchResponse struct {
	Project  string `json:"project"`
	Model    string `json:"model"`
	Fallback bool   `json:"fallback"`
	Keyword  bool   `json:"keyword"`
	// Degraded is true when the embedding circuit was open on the server and
	// keyword results were served; RetryAfterMS hints when to retry semantic
	// search. Absent (false/0) on older servers.
	Degraded     bool        `json:"degraded"`
	RetryAfterMS int64       `json:"retry_after_ms"`
	TookMS       int64       `json:"took_ms"`
	Results      []SearchHit `json:"results"`
}

// MultiSearchResponse is the tenant-scoped result envelope for a search over
// several projects. Each hit includes project provenance and the RRF score
// used for cross-project ordering.
type MultiSearchResponse struct {
	Fallback     bool        `json:"fallback"`
	Keyword      bool        `json:"keyword"`
	Degraded     bool        `json:"degraded"`
	RetryAfterMS int64       `json:"retry_after_ms"`
	TookMS       int64       `json:"took_ms"`
	ProjectCount int         `json:"project_count"`
	SkippedCount int         `json:"skipped_count"`
	Results      []SearchHit `json:"results"`
}

type Project struct {
	TenantID    int    `json:"tenant_id,omitempty"`
	WorkspaceID int    `json:"workspace_id,omitempty"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	Status      string `json:"status"`
	SourceType  string `json:"source_type"`
	GitURL      string `json:"git_url,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Identity    string `json:"identity,omitempty"`
	Path        string `json:"path,omitempty"`
	PrivacyMode string `json:"privacy_mode,omitempty"`
}

type RuntimeEdge struct {
	TenantID          int       `json:"tenant_id,omitempty"`
	WorkspaceID       int       `json:"workspace_id,omitempty"`
	SourceProjectID   int       `json:"source_project_id,omitempty"`
	SourceProjectName string    `json:"source_project,omitempty"`
	TargetProjectID   int       `json:"target_project_id,omitempty"`
	TargetProjectName string    `json:"target_project"`
	SourceComponent   string    `json:"source_component,omitempty"`
	TargetComponent   string    `json:"target_component,omitempty"`
	Protocol          string    `json:"protocol,omitempty"`
	Environment       string    `json:"environment,omitempty"`
	RequestCount      int64     `json:"request_count"`
	ErrorCount        int64     `json:"error_count"`
	P95LatencyMS      float64   `json:"p95_latency_ms"`
	FirstSeen         time.Time `json:"first_seen,omitempty"`
	LastSeen          time.Time `json:"last_seen,omitempty"`
}

type TenantQuota struct {
	TenantID        int    `json:"tenant_id"`
	Plan            string `json:"plan"`
	MaxProjects     int64  `json:"max_projects"`
	MaxRuntimeEdges int64  `json:"max_runtime_edges"`
}

type TenantUsage struct {
	TenantID     int   `json:"tenant_id"`
	Projects     int64 `json:"projects"`
	RuntimeEdges int64 `json:"runtime_edges"`
}

type UsageResponse struct {
	Quota TenantQuota `json:"quota"`
	Usage TenantUsage `json:"usage"`
}

type Tenant struct {
	ID   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type Workspace struct {
	ID       int    `json:"id"`
	TenantID int    `json:"tenant_id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
}

type Dependency struct {
	Ecosystem       string `json:"ecosystem"`
	Name            string `json:"name"`
	NormalizedName  string `json:"normalized_name"`
	Constraint      string `json:"constraint,omitempty"`
	ResolvedVersion string `json:"resolved_version,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Source          string `json:"source,omitempty"`
	Manifest        string `json:"manifest"`
	Direct          bool   `json:"direct"`
}

type DependencyUsage struct {
	ProjectID       int    `json:"project_id"`
	ProjectName     string `json:"project_name"`
	Ecosystem       string `json:"ecosystem"`
	Name            string `json:"name"`
	NormalizedName  string `json:"normalized_name"`
	Constraint      string `json:"constraint,omitempty"`
	ResolvedVersion string `json:"resolved_version,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Direct          bool   `json:"direct"`
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

type DependencyResolveResponse struct {
	Project string `json:"project"`
	Mode    string `json:"mode"`
	Status  string `json:"status"`
	JobID   int    `json:"job_id,omitempty"`
	Submit  string `json:"submit,omitempty"`
}

type DependencySubmitResponse struct {
	Project string `json:"project"`
	Status  string `json:"status"`
	Count   int    `json:"count"`
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

// ListTenants returns tenants visible to the authenticated principal.
func (c *Client) ListTenants(ctx context.Context) ([]Tenant, error) {
	var out struct {
		Tenants []Tenant `json:"tenants"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/tenants", nil, &out); err != nil {
		return nil, err
	}
	return out.Tenants, nil
}

// CreateTenant creates an organization. The authenticated user is added as its
// owner when the server has a user-bound token.
func (c *Client) CreateTenant(ctx context.Context, slug, name string) (*Tenant, error) {
	var out Tenant
	if err := c.do(ctx, http.MethodPost, "/api/v1/tenants", map[string]string{
		"slug": slug, "name": name,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListWorkspaces returns workspaces visible in the active tenant.
func (c *Client) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	var out struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/workspaces", nil, &out); err != nil {
		return nil, err
	}
	return out.Workspaces, nil
}

// Usage returns the active tenant's quota and current counters.
func (c *Client) Usage(ctx context.Context) (*UsageResponse, error) {
	var out UsageResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/usage", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateWorkspace creates a project portfolio inside the active tenant.
func (c *Client) CreateWorkspace(ctx context.Context, slug, name string) (*Workspace, error) {
	var out Workspace
	if err := c.do(ctx, http.MethodPost, "/api/v1/workspaces", map[string]string{"slug": slug, "name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Healthz reports whether the server is reachable.
func (c *Client) Healthz(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil, nil)
}

// UsageReport is the JSON shape of GET /api/v1/search-usage — product-level
// search analytics (counts by project/source/outcome), not tenant billing
// quota (see UsageResponse/Usage for that).
type UsageReport struct {
	GeneratedAt string `json:"generated_at"`
	SinceDays   int    `json:"since_days"`
	Project     string `json:"project,omitempty"`
	Summary     string `json:"summary"`
	Total       int    `json:"total"`
	ByProject   []struct {
		Key   string `json:"key"`
		Count int    `json:"count"`
	} `json:"by_project"`
	BySource []struct {
		Key   string `json:"key"`
		Count int    `json:"count"`
	} `json:"by_source"`
	ByOutcome []struct {
		Key   string `json:"key"`
		Count int    `json:"count"`
	} `json:"by_outcome"`
	Rates struct {
		OK       float64 `json:"ok"`
		Empty    float64 `json:"empty"`
		Fallback float64 `json:"fallback"`
		Error    float64 `json:"error"`
		MCP      float64 `json:"mcp"`
		CLI      float64 `json:"cli"`
	} `json:"rates"`
	Findings []struct {
		Kind     string `json:"kind"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"findings"`
	BlindSpots []string `json:"blind_spots"`
}

// SearchUsage fetches the product search-usage analytics report from the
// server (GET /api/v1/search-usage). Not to be confused with Usage, which
// returns the active tenant's billing quota/counters.
func (c *Client) SearchUsage(ctx context.Context, days int, project string) (*UsageReport, error) {
	if days <= 0 {
		days = 30
	}
	path := "/api/v1/search-usage?days=" + strconv.Itoa(days)
	if project != "" {
		path += "&project=" + url.QueryEscape(project)
	}
	var out UsageReport
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

// MultiSearchParams carries the optional knobs for Client.SearchMulti.
type MultiSearchParams struct {
	Projects      []string // project names; empty is valid only when All is true
	Identities    []string // stable Git/path identities for agent integrations
	All           bool
	TopK          int
	Keyword       bool
	Graph         bool
	GraphDepth    int
	MaxPerFile    int
	MaxPerProject int
}

// requireProject rejects empty project path segments before they become
// /api/v1/projects//… which reverse proxies often rewrite into a different
// route (HTTP 405).
func requireProject(project string) error {
	if strings.TrimSpace(project) == "" {
		return fmt.Errorf("semidx: project name is required")
	}
	return nil
}

// Search runs a search over a project. With Params.Keyword the server searches
// by keyword only and never contacts the embedding provider (the remote-mode
// equivalent of the CLI's --keyword); otherwise it runs a semantic search that
// transparently falls back to keyword when embeddings are unavailable.
func (c *Client) Search(ctx context.Context, project, query string, p SearchParams) (*SearchResponse, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	body := map[string]any{"query": query, "top_k": p.TopK, "model": p.Model, "keyword": p.Keyword, "graph": p.Graph, "graph_depth": p.GraphDepth}
	var out SearchResponse
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/search", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchMulti searches across selected projects or all projects in the active
// workspace. Project names are resolved server-side, so callers do not need to
// know Git/path identities.
func (c *Client) SearchMulti(ctx context.Context, query string, p MultiSearchParams) (*MultiSearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("semidx: query is required")
	}
	if !p.All && len(p.Projects) == 0 && len(p.Identities) == 0 {
		return nil, fmt.Errorf("semidx: projects are required unless all is true")
	}
	body := map[string]any{
		"query": query, "projects": p.Projects, "identities": p.Identities, "all": p.All,
		"top_k": p.TopK, "keyword": p.Keyword, "graph": p.Graph,
		"graph_depth": p.GraphDepth, "max_per_file": p.MaxPerFile,
		"max_per_project": p.MaxPerProject,
	}
	var out MultiSearchResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/search", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDependencies returns the normalized manifest catalog for a project.
func (c *Client) ListDependencies(ctx context.Context, project string) ([]Dependency, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	var out struct {
		Dependencies []Dependency `json:"dependencies"`
	}
	if err := c.do(ctx, http.MethodGet, projectsPath+esc(project)+"/dependencies", nil, &out); err != nil {
		return nil, err
	}
	return out.Dependencies, nil
}

// SharedDependencies returns dependency occurrences in other projects of the
// active workspace that share an ecosystem and normalized package name.
func (c *Client) SharedDependencies(ctx context.Context, project string) ([]DependencyUsage, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	var out struct {
		Dependencies []DependencyUsage `json:"dependencies"`
	}
	if err := c.do(ctx, http.MethodGet, projectsPath+esc(project)+"/dependencies/shared", nil, &out); err != nil {
		return nil, err
	}
	return out.Dependencies, nil
}

// ListRuntimeEdges returns observed outbound communication for a project.
func (c *Client) ListRuntimeEdges(ctx context.Context, project string) ([]RuntimeEdge, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	var out struct {
		Edges []RuntimeEdge `json:"edges"`
	}
	if err := c.do(ctx, http.MethodGet, projectsPath+esc(project)+"/runtime-edges", nil, &out); err != nil {
		return nil, err
	}
	return out.Edges, nil
}

// SubmitRuntimeEdges adds customer-agent or OpenTelemetry-derived observations
// without uploading source files.
func (c *Client) SubmitRuntimeEdges(ctx context.Context, project string, edges []RuntimeEdge) (int, error) {
	if err := requireProject(project); err != nil {
		return 0, err
	}
	var out struct {
		Accepted int `json:"accepted"`
	}
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/runtime-edges", map[string]any{"edges": edges}, &out); err != nil {
		return 0, err
	}
	return out.Accepted, nil
}

// ListRuntimeGraph returns the tenant/workspace portfolio communication graph.
func (c *Client) ListRuntimeGraph(ctx context.Context, limit int) ([]RuntimeEdge, error) {
	path := "/api/v1/runtime-graph"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out struct {
		Edges []RuntimeEdge `json:"edges"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Edges, nil
}

// ResolveDependencies asks a managed worker to run native package tooling, or
// returns the submit contract for a customer agent when mode is "agent".
func (c *Client) ResolveDependencies(ctx context.Context, project, mode string) (*DependencyResolveResponse, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	var out DependencyResolveResponse
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/dependencies/resolve", map[string]string{"mode": mode}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubmitDependencies stores a resolution result produced by a customer agent.
func (c *Client) SubmitDependencies(ctx context.Context, project string, deps []Dependency, source string) (*DependencySubmitResponse, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	var out DependencySubmitResponse
	if err := c.do(ctx, http.MethodPost, projectsPath+esc(project)+"/dependencies/submit", map[string]any{"dependencies": deps, "source": source}, &out); err != nil {
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
	if err := requireProject(name); err != nil {
		return nil, err
	}
	var out Project
	if err := c.do(ctx, http.MethodGet, projectsPath+esc(name), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateProject registers a project. sourceType is "push" or "git".
func (c *Client) CreateProject(ctx context.Context, name, model, sourceType, gitURL, branch string) (*Project, error) {
	return c.CreateProjectWithPrivacy(ctx, name, model, sourceType, gitURL, branch, "")
}

// CreateProjectWithPrivacy registers a project and optionally selects its
// cloud/hybrid/edge data-routing policy in the same request.
func (c *Client) CreateProjectWithPrivacy(ctx context.Context, name, model, sourceType, gitURL, branch, privacyMode string) (*Project, error) {
	body := map[string]any{
		"name":  name,
		"model": model,
		"source": map[string]string{
			"type": sourceType, "url": gitURL, "branch": branch,
		},
	}
	if privacyMode != "" {
		body["privacy_mode"] = privacyMode
	}
	var out Project
	if err := c.do(ctx, http.MethodPost, "/api/v1/projects", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetProjectPrivacy changes the persisted project embedding policy.
func (c *Client) SetProjectPrivacy(ctx context.Context, project, mode string) (*Project, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	var out Project
	if err := c.do(ctx, http.MethodPut, projectsPath+esc(project)+"/privacy", map[string]string{"mode": mode}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Status returns the indexing status and file count for a project.
func (c *Client) Status(ctx context.Context, project string) (*StatusResponse, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
	var out StatusResponse
	if err := c.do(ctx, http.MethodGet, projectsPath+esc(project)+"/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteProject removes a project.
func (c *Client) DeleteProject(ctx context.Context, name string) error {
	if err := requireProject(name); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, projectsPath+esc(name), nil, nil)
}

// EnqueueJob queues an index job and returns its id.
func (c *Client) EnqueueJob(ctx context.Context, project, jobType string) (int, error) {
	if err := requireProject(project); err != nil {
		return 0, err
	}
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
	if err := requireProject(project); err != nil {
		return nil, err
	}
	var out Job
	path := projectsPath + esc(project) + "/jobs/" + strconv.Itoa(id)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FilesDiff reports which of the given path→hash files are stale or deleted.
func (c *Client) FilesDiff(ctx context.Context, project string, hashes map[string]string) (*DiffResponse, error) {
	if err := requireProject(project); err != nil {
		return nil, err
	}
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
	if err := requireProject(project); err != nil {
		return nil, err
	}
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
	if err := requireProject(project); err != nil {
		return 0, err
	}
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
	if c.tenant != "" {
		req.Header.Set("X-Semidx-Tenant", c.tenant)
	}
	if c.workspace != "" {
		req.Header.Set("X-Semidx-Workspace", c.workspace)
	}
	if c.ClientSource != "" {
		req.Header.Set(HeaderClientSource, c.ClientSource)
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
	if c.tenant != "" {
		req.Header.Set("X-Semidx-Tenant", c.tenant)
	}
	if c.workspace != "" {
		req.Header.Set("X-Semidx-Workspace", c.workspace)
	}
	if c.ClientSource != "" {
		req.Header.Set(HeaderClientSource, c.ClientSource)
	}
	return c.http.Do(req)
}
