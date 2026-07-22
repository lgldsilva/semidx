package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

// clientBackend adapts the HTTP API client to the Backend interface (remote mode).
type clientBackend struct{ c *client.Client }

// NewClientBackend wraps a semidx API client as an MCP Backend (remote mode).
func NewClientBackend(c *client.Client) Backend { return &clientBackend{c: c} }

func (b *clientBackend) Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error) {
	resp, err := b.c.Search(ctx, project, query, client.SearchParams{
		Model: model, TopK: topK, Graph: graph, GraphDepth: graphDepth,
	})
	if err != nil {
		return nil, err
	}
	out := &SearchOutput{
		Project: resp.Project, Fallback: resp.Fallback,
		Degraded: resp.Degraded, RetryAfterMS: resp.RetryAfterMS,
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, Hit{
			Path: r.Path, StartLine: r.StartLine, EndLine: r.EndLine,
			Score: r.Score, Content: r.Content,
			Confidence: r.Confidence, Symbol: r.Symbol,
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
		RetryAfter:   time.Duration(resp.RetryAfterMS) * time.Millisecond,
		ProjectCount: resp.ProjectCount, SkippedCount: resp.SkippedCount,
	}
	for _, hit := range resp.Results {
		out.Results = append(out.Results, search.MultiResult{
			SearchResult: store.SearchResult{FilePath: hit.Path, StartLine: hit.StartLine,
				EndLine: hit.EndLine, Score: hit.Score, Content: hit.Content},
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
			Name: p.Name, SourceType: p.SourceType, GitURL: p.GitURL, Status: p.Status, Model: p.Model,
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

var _ MultiSearchBackend = (*clientBackend)(nil)
