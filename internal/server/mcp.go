package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/codeintel"
	"github.com/lgldsilva/semidx/internal/mcpserver"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// EnableMCPHTTP mounts the MCP server over Streamable HTTP at /mcp (default
// OFF; the CLI gates it behind --mcp-http / SEMIDX_MCP_HTTP). The endpoint is
// stateless — every POST carries one JSON-RPC exchange — and runs in-process
// over the same store/search service as the REST API, so no extra process or
// port is needed.
//
// Security: the route is registered behind the same bearer auth as the REST
// API (read scope to reach the endpoint; semantic_reindex additionally
// requires write). Never expose the listen address without auth in front —
// the endpoint is only as safe as the tokens guarding it.
func (s *Server) EnableMCPHTTP() {
	backend := &serverBackend{s: s}
	s.mcpHTTP = mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpserver.New(backend) },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}

// serverBackend adapts the running server (store + search service) to
// mcpserver.Backend, so the MCP tools execute in-process instead of proxying
// the HTTP API through another client.
type serverBackend struct{ s *Server }

func (b *serverBackend) Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*mcpserver.SearchOutput, error) {
	if graphDepth <= 0 {
		graphDepth = search.DefaultGraphDepth
	}
	resp, err := b.s.search.Search(ctx, search.Request{
		Project: project, Query: query, Model: model, TopK: topK,
		Graph: graph, GraphMaxDepth: search.ClampGraphDepth(graphDepth),
	})
	if err != nil {
		return nil, b.safeErr("search", project, err)
	}
	out := &mcpserver.SearchOutput{
		Project: resp.Project.Name, Fallback: resp.Fallback,
		Degraded: resp.Degraded, RetryAfterMS: resp.RetryAfter.Milliseconds(),
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, mcpserver.Hit{
			Path: r.FilePath, StartLine: r.StartLine, EndLine: r.EndLine,
			Score: r.Score, Content: r.Content,
			Confidence: r.Confidence, Symbol: r.Symbol,
			Stale: r.Stale, IndexedAt: r.IndexedAt,
		})
	}
	return out, nil
}

func (b *serverBackend) Projects(ctx context.Context) ([]mcpserver.ProjectInfo, error) {
	projects, err := b.s.store.ListProjects(ctx, 0, 0)
	if err != nil {
		return nil, b.safeErr("list projects", "", err)
	}
	out := make([]mcpserver.ProjectInfo, 0, len(projects))
	for _, p := range projects {
		out = append(out, mcpserver.ProjectInfo{
			Name: p.Name, SourceType: p.SourceType, GitURL: p.GitURL, Status: p.Status, Model: p.Model,
		})
	}
	return out, nil
}

func (b *serverBackend) Status(ctx context.Context, project string) (*mcpserver.StatusInfo, error) {
	proj, err := b.s.store.GetProject(ctx, project)
	if err != nil {
		return nil, b.safeErr("status", project, err)
	}
	count, err := b.s.store.CountProjectFiles(ctx, proj.ID)
	if err != nil {
		return nil, b.safeErr("count files", project, err)
	}
	return &mcpserver.StatusInfo{
		Name: proj.Name, SourceType: proj.SourceType, Identity: proj.Identity,
		Status: proj.Status, Model: proj.Model, TotalFiles: count,
	}, nil
}

// Reindex enqueues a re-index job. The /mcp route only demands the read scope
// (search must work with a read-only token), so the write check happens here,
// against the scopes authed stored in the context — the same rule the REST
// route POST /index-jobs enforces. The error is returned in-band for the agent.
func (b *serverBackend) Reindex(ctx context.Context, project, jobType string) (string, error) {
	if !hasScope(ctx, "write") {
		return "", errors.New("semantic_reindex requires a token with the write (or admin) scope")
	}
	if jobType == "" {
		jobType = "full"
	}
	if jobType != "full" && jobType != "git_history" {
		return "", errors.New("type must be 'full' or 'git_history'")
	}
	proj, err := b.s.store.GetProject(ctx, project)
	if err != nil {
		return "", b.safeErr("reindex", project, err)
	}
	id, err := b.s.store.EnqueueJob(ctx, proj.ID, jobType)
	if err != nil {
		return "", b.safeErr("enqueue job", project, err)
	}
	b.s.jobsQueued.Inc()
	return fmt.Sprintf("Queued %s re-index job #%d for project %q.", jobType, id, project), nil
}

func (b *serverBackend) Capabilities() agent.Capabilities {
	return agent.Capabilities{Flags: agent.CapRemoteIndex}
}

// safeErr collapses backend errors to agent-safe messages (REQ-SRCH-08): a
// missing project stays actionable, anything else may carry DSNs/pgx internals
// and is logged server-side instead of surfaced.
func (b *serverBackend) safeErr(op, project string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("project not found")
	}
	b.s.log.Error("mcp "+op+" failed", "project", project, "err", err)
	return errors.New(op + " failed")
}

// Code-intelligence tools are standalone/local-only for now (no server HTTP
// endpoints yet). Return a structured error the MCP handler surfaces in-band.
func (b *serverBackend) Callers(_ context.Context, _, _ string, _ int) (*codeintel.CallersResult, error) {
	return nil, fmt.Errorf("code-intelligence tool %q is available in standalone/local mode only; remote server support is not yet implemented", "semantic_callers")
}

func (b *serverBackend) Explain(_ context.Context, _, _ string, _ int) (*codeintel.ExplainResult, error) {
	return nil, fmt.Errorf("code-intelligence tool %q is available in standalone/local mode only; remote server support is not yet implemented", "semantic_explain")
}

func (b *serverBackend) Impact(_ context.Context, _, _ string, _ int, _ int) (*codeintel.ImpactResult, error) {
	return nil, fmt.Errorf("code-intelligence tool %q is available in standalone/local mode only; remote server support is not yet implemented", "semantic_impact")
}

func (b *serverBackend) DeadCode(_ context.Context, _ string) (*codeintel.DeadCodeResult, error) {
	return nil, fmt.Errorf("code-intelligence tool %q is available in standalone/local mode only; remote server support is not yet implemented", "semantic_deadcode")
}

func (b *serverBackend) Diff(_ context.Context, _ string) (*codeintel.DiffResult, error) {
	return nil, fmt.Errorf("code-intelligence tool %q is available in standalone/local mode only; remote server support is not yet implemented", "semantic_diff")
}
