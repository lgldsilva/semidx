package mcpserver

import (
	"context"
	"fmt"

	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// projectLister is the subset of the local store the MCP backend needs to list
// projects (satisfied by store.IndexStore).
type projectLister interface {
	ListProjects(ctx context.Context, limit, offset int) ([]store.Project, error)
}

// localBackend adapts the standalone index (search service + local store) to the
// Backend interface, so `semidx mcp` works without a server.
type localBackend struct {
	svc         *search.Service
	projects    projectLister
	keywordOnly bool
}

// NewLocalBackend wraps a local search service and store as an MCP Backend
// (standalone mode). keywordOnly mirrors the index's embedding mode.
func NewLocalBackend(svc *search.Service, projects projectLister, keywordOnly bool) Backend {
	return &localBackend{svc: svc, projects: projects, keywordOnly: keywordOnly}
}

func (b *localBackend) Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error) {
	// A standalone MCP server is not tied to a git worktree, so no worktree filter
	// is applied — it searches the whole project index.
	resp, err := b.svc.Search(ctx, search.Request{
		Project: project, Query: query, Model: model, TopK: topK, KeywordOnly: b.keywordOnly,
		Graph: graph, GraphMaxDepth: graphDepth,
	})
	if err != nil {
		return nil, err
	}
	out := &SearchOutput{Project: resp.Project.Name, Fallback: resp.Fallback}
	for _, r := range resp.Results {
		out.Results = append(out.Results, Hit{Path: r.FilePath, StartLine: r.StartLine, Score: r.Score, Content: r.Content})
	}
	return out, nil
}

func (b *localBackend) Projects(ctx context.Context) ([]ProjectInfo, error) {
	projects, err := b.projects.ListProjects(ctx, 0, 0)
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

func (b *localBackend) Reindex(_ context.Context, project, _ string) (string, error) {
	// Reindex needs the project's source on disk (the embedder, worker pool and
	// walk live in the CLI, not here). In standalone mode that path is the CLI.
	return "", fmt.Errorf("standalone mode: reindex %q with `semidx index <path>` (or `--local`); MCP reindex is available in server mode", project)
}
