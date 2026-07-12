package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/repotools"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// projectLister is the subset of the local store the MCP backend needs to list
// projects and query their file status (satisfied by store.IndexStore).
type projectLister interface {
	ListProjects(ctx context.Context, limit, offset int) ([]store.Project, error)
	GetProject(ctx context.Context, name string) (*store.Project, error)
	GetProjectByIdentity(ctx context.Context, identity string) (*store.Project, error)
	CountProjectFiles(ctx context.Context, projectID int) (int, error)
	ListFileHashes(ctx context.Context, projectID int) (map[string]string, error)
}

// localBackend adapts the standalone index (search service + local store) to the
// Backend interface, so `semidx mcp` works without a server.
type localBackend struct {
	svc         *search.Service
	projects    projectLister
	keywordOnly bool
	caps        agent.Capabilities
}

// NewLocalBackend wraps a local search service and store as an MCP Backend
// (standalone mode). keywordOnly mirrors the index's embedding mode. caps
// describes what the local runtime offers (local git, chat LLM, etc.).
func NewLocalBackend(svc *search.Service, projects projectLister, keywordOnly bool, caps agent.Capabilities) Backend {
	if caps.Flags == 0 {
		caps.Flags = agent.CapLocalGit | agent.CapIndexLocal
	}
	return &localBackend{svc: svc, projects: projects, keywordOnly: keywordOnly, caps: caps}
}

func (b *localBackend) Search(ctx context.Context, project, query, model string, topK int, graph bool, graphDepth int) (*SearchOutput, error) {
	// A standalone MCP server is not tied to a git worktree, so no worktree filter
	// is applied — it searches the whole project index.
	resp, err := b.svc.Search(ctx, search.Request{
		Project: project, Query: query, Model: model, TopK: topK, KeywordOnly: b.keywordOnly,
		Graph: graph, GraphMaxDepth: graphDepth,
	})
	if err != nil {
		return nil, safeSearchErr(err)
	}
	out := &SearchOutput{Project: resp.Project.Name, Fallback: resp.Fallback}
	for _, r := range resp.Results {
		out.Results = append(out.Results, Hit{Path: r.FilePath, StartLine: r.StartLine, EndLine: r.EndLine, Score: r.Score, Content: r.Content})
	}
	return out, nil
}

// safeSearchErr maps a raw search error to one safe to surface to the agent
// (REQ-SRCH-08). "project not found" stays actionable; anything else carries
// database/provider internals (DSNs, pgx errors, provider response bodies), so
// it is logged to stderr and collapsed to a generic message. The remote backend
// needs no such guard — the server already sanitizes before the wire.
func safeSearchErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("project not found")
	}
	slog.Error("mcp search failed", "err", err)
	return errors.New("search failed")
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

func (b *localBackend) Status(ctx context.Context, project string) (*StatusInfo, error) {
	// Try to resolve the project by name first, then by identity.
	proj, err := b.projects.GetProject(ctx, project)
	if err != nil {
		// Fallback: try identity lookup (the project may be a git identity).
		proj, err = b.projects.GetProjectByIdentity(ctx, project)
		if err != nil {
			return nil, fmt.Errorf("project %q not found", project)
		}
	}
	count, err := b.projects.CountProjectFiles(ctx, proj.ID)
	if err != nil {
		return nil, fmt.Errorf("count files: %w", err)
	}
	return &StatusInfo{
		Name:       proj.Name,
		SourceType: proj.SourceType,
		Identity:   proj.Identity,
		Status:     proj.Status,
		Model:      proj.Model,
		TotalFiles: count,
	}, nil
}

func (b *localBackend) Reindex(_ context.Context, project, _ string) (string, error) {
	// Reindex needs the project's source on disk (the embedder, worker pool and
	// walk live in the CLI, not here). In standalone mode that path is the CLI.
	return "", fmt.Errorf("standalone mode: reindex %q with `semidx index <path>` (or `--local`); MCP reindex is available in server mode", project)
}

// Capabilities reports what the local backend offers.
func (b *localBackend) Capabilities() agent.Capabilities { return b.caps }

// resolveProjectPath resolves a project name or identity to a local filesystem
// path. Returns the path and an error if the project is not a local checkout.
func (b *localBackend) resolveProjectPath(ctx context.Context, project string) (string, error) {
	p, err := b.projects.GetProject(ctx, project)
	if err != nil {
		p, err = b.projects.GetProjectByIdentity(ctx, project)
		if err != nil {
			return "", fmt.Errorf("project %q not found locally", project)
		}
	}
	if p.Path == "" {
		return "", fmt.Errorf("project %q has no local path (remote/server clone)", project)
	}
	return p.Path, nil
}

// Worktrees lists git worktrees for a registered project.
func (b *localBackend) Worktrees(ctx context.Context, project string) ([]repotools.Worktree, error) {
	root, err := b.resolveProjectPath(ctx, project)
	if err != nil {
		return nil, err
	}
	return repotools.ListWorktrees(ctx, root)
}

// Branches lists git branches for a registered project.
func (b *localBackend) Branches(ctx context.Context, project string, remote bool) ([]repotools.Branch, error) {
	root, err := b.resolveProjectPath(ctx, project)
	if err != nil {
		return nil, err
	}
	return repotools.ListBranches(ctx, root, remote)
}

// GitStatus returns the working tree state for a registered project.
func (b *localBackend) GitStatus(ctx context.Context, project string) (*repotools.RepoStatus, error) {
	root, err := b.resolveProjectPath(ctx, project)
	if err != nil {
		return nil, err
	}
	return repotools.Status(ctx, root)
}

// SearchMulti searches across multiple project identities and fuses results.
func (b *localBackend) SearchMulti(ctx context.Context, req search.MultiScopeRequest) (*search.MultiResponse, error) {
	if b.keywordOnly {
		req.KeywordOnly = true
	}
	return b.svc.SearchMulti(ctx, req)
}

// Compile-time check: *localBackend satisfies GitBackend and MultiSearchBackend.
var _ GitBackend = (*localBackend)(nil)
var _ MultiSearchBackend = (*localBackend)(nil)
