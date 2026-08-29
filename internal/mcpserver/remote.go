package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/codeintel"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/searchtargets"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/usage"
	"github.com/lgldsilva/semidx/pkg/client"
)

// clientBackend adapts the HTTP API client to the Backend interface (remote mode).
type clientBackend struct{ c *client.Client }

// NewClientBackend wraps a semidx API client as an MCP Backend (remote mode).
// Always forces ClientSource=mcp so usage is never mis-attributed when the
// underlying client was built for CLI/SDK traffic.
func NewClientBackend(c *client.Client) Backend {
	if c != nil {
		c.ClientSource = string(usage.SourceMCP)
	}
	return &clientBackend{c: c}
}

// ResolveCWDProject maps the process working directory onto a server project
// (git origin, enclosing path, then directory name) so MCP "project": "." and
// omitted project (when no default is set) match the CLI.
func (b *clientBackend) ResolveCWDProject(ctx context.Context) (string, error) {
	if b == nil || b.c == nil {
		return "", fmt.Errorf("remote MCP has no API client")
	}
	p, err := searchtargets.ResolveRemoteProject(ctx, b.c, ".")
	if err != nil {
		return "", err
	}
	return p.Name, nil
}

func (b *clientBackend) Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error) {
	resp, err := b.c.Search(ctx, project, query, client.SearchParams{
		Model: model, TopK: topK, Graph: graph, GraphDepth: graphDepth,
	})
	if err != nil {
		return nil, err
	}
	out := &SearchOutput{
		Project: resp.Project, Model: resp.Model, Route: resp.Route, Keyword: resp.Keyword, Fallback: resp.Fallback,
		Degraded: resp.Degraded, RetryAfterMS: resp.RetryAfterMS, FallbackReason: resp.FallbackReason,
		TookMS: resp.TookMS,
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, Hit{
			Path: r.Path, StartLine: r.StartLine, EndLine: r.EndLine,
			Score: r.Score, Content: r.Content,
			Confidence: r.Confidence, Symbol: r.Symbol,
			Stale: r.Stale, IndexedAt: r.IndexedAt,
			Source: r.Source, GraphDepth: r.GraphDepth,
		})
	}
	return out, nil
}

func (b *clientBackend) SearchMulti(ctx context.Context, req search.MultiScopeRequest) (*search.MultiResponse, error) {
	resp, err := b.c.SearchMulti(ctx, req.Query, client.MultiSearchParams{
		Projects: req.Projects, Identities: req.Identities, All: req.All, TopK: req.TopK, Keyword: req.KeywordOnly,
		Graph: req.Graph, GraphDepth: req.GraphMaxDepth,
		MaxPerFile: req.MaxPerFile, MaxPerProject: req.MaxPerProject,
	})
	if err != nil {
		return nil, err
	}
	out := &search.MultiResponse{
		Fallback: resp.Fallback, Keyword: resp.Keyword, Degraded: resp.Degraded,
		Route: resp.Route, Routes: resp.Routes,
		RetryAfter:   time.Duration(resp.RetryAfterMS) * time.Millisecond,
		ProjectCount: resp.ProjectCount, SkippedCount: resp.SkippedCount,
	}
	for _, hit := range resp.Results {
		out.Results = append(out.Results, search.MultiResult{
			SearchResult: store.SearchResult{FilePath: hit.Path, StartLine: hit.StartLine,
				EndLine: hit.EndLine, Score: hit.Score, Content: hit.Content,
				Confidence: hit.Confidence, Symbol: hit.Symbol,
				Stale: hit.Stale, IndexedAt: hit.IndexedAt,
				Source: hit.Source, GraphDepth: hit.GraphDepth},
			Project: hit.Project, SourceRank: hit.SourceRank, FusionScore: hit.FusionScore,
		})
	}
	return out, nil
}

func (b *clientBackend) Projects(ctx context.Context) ([]ProjectInfo, error) {
	projects, err := b.c.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectInfo, 0, len(projects))
	for _, p := range projects {
		out = append(out, ProjectInfo{
			Name: p.Name, SourceType: p.SourceType, GitURL: gitmeta.RedactURL(p.GitURL), Status: p.Status, Model: p.Model,
		})
	}
	return out, nil
}

func (b *clientBackend) Status(ctx context.Context, project string) (*StatusInfo, error) {
	resp, err := b.c.Status(ctx, project)
	if err != nil {
		return nil, err
	}
	return &StatusInfo{
		Name:       resp.Name,
		SourceType: resp.SourceType,
		Identity:   resp.Identity,
		Status:     resp.Status,
		Model:      resp.Model,
		TotalFiles: resp.TotalFiles,
	}, nil
}

func (b *clientBackend) Reindex(ctx context.Context, project, jobType string) (string, error) {
	id, err := b.c.EnqueueJob(ctx, project, jobType)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Queued %s re-index job #%d for project %q.", jobType, id, project), nil
}

func (b *clientBackend) Capabilities() agent.Capabilities {
	return agent.Capabilities{Flags: agent.CapRemoteIndex}
}

func (b *clientBackend) Callers(_ context.Context, _, _ string, _ int) (*codeintel.CallersResult, error) {
	return nil, ErrCodeIntelStandaloneOnly(toolSemanticCallers)
}

func (b *clientBackend) Explain(_ context.Context, _, _ string, _ int) (*codeintel.ExplainResult, error) {
	return nil, ErrCodeIntelStandaloneOnly(toolSemanticExplain)
}

func (b *clientBackend) Impact(_ context.Context, _, _ string, _ int, _ int) (*codeintel.ImpactResult, error) {
	return nil, ErrCodeIntelStandaloneOnly(toolSemanticImpact)
}

func (b *clientBackend) DeadCode(_ context.Context, _ string) (*codeintel.DeadCodeResult, error) {
	return nil, ErrCodeIntelStandaloneOnly(toolSemanticDeadCode)
}

func (b *clientBackend) Diff(_ context.Context, _ string) (*codeintel.DiffResult, error) {
	return nil, ErrCodeIntelStandaloneOnly(toolSemanticDiff)
}

var _ MultiSearchBackend = (*clientBackend)(nil)
