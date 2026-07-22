package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/codeintel"
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

type graphLister interface {
	FetchGraphNeighbors(ctx context.Context, projectID int) (map[string][]string, error)
	FetchGraphPathsBFS(ctx context.Context, projectID int, seedPaths []string, maxDepth int) (map[string]int, error)
}

// localBackend adapts the standalone index (search service + local store) to the
// Backend interface, so `semidx mcp` works without a server.
type localBackend struct {
	svc         *search.Service
	idx         store.IndexStore
	keywordOnly bool
	caps        agent.Capabilities
}

// NewLocalBackend wraps a local search service and store as an MCP Backend
// (standalone mode). keywordOnly mirrors the index's embedding mode. caps
// describes what the local runtime offers (local git, chat LLM, etc.).
func NewLocalBackend(svc *search.Service, idx store.IndexStore, keywordOnly bool, caps agent.Capabilities) Backend {
	if caps.Flags == 0 {
		caps.Flags = agent.CapLocalGit | agent.CapIndexLocal
	}
	return &localBackend{svc: svc, idx: idx, keywordOnly: keywordOnly, caps: caps}
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
	out := &SearchOutput{
		Project: resp.Project.Name, Fallback: resp.Fallback,
		Degraded: resp.Degraded, RetryAfterMS: resp.RetryAfter.Milliseconds(),
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, Hit{
			Path: r.FilePath, StartLine: r.StartLine, EndLine: r.EndLine,
			Score: r.Score, Content: r.Content,
			Confidence: r.Confidence, Symbol: r.Symbol,
		})
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
	projects, err := b.idx.ListProjects(ctx, 0, 0)
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
	proj, err := b.idx.GetProject(ctx, project)
	if err != nil {
		// Fallback: try identity lookup (the project may be a git identity).
		proj, err = b.idx.GetProjectByIdentity(ctx, project)
		if err != nil {
			return nil, fmt.Errorf("project %q not found", project)
		}
	}
	count, err := b.idx.CountProjectFiles(ctx, proj.ID)
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

// resolveProject resolves a project name or identity to a store.Project.
func (b *localBackend) resolveProject(ctx context.Context, project string) (*store.Project, error) {
	p, err := b.idx.GetProject(ctx, project)
	if err != nil {
		p, err = b.idx.GetProjectByIdentity(ctx, project)
		if err != nil {
			return nil, fmt.Errorf("project %q not found locally", project)
		}
	}
	return p, nil
}

// graphProject resolves a project and asserts the backing store implements
// graphLister. It is the shared preamble of Neighbors and Trace: both need the
// project's row ID and a graph-capable handle on the store, and both report
// the same "graph not supported" error when the store lacks the methods.
func (b *localBackend) graphProject(ctx context.Context, project string) (*store.Project, graphLister, error) {
	p, err := b.resolveProject(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	gl, ok := b.idx.(graphLister)
	if !ok {
		return nil, nil, fmt.Errorf("graph operations are not supported by the current store")
	}
	return p, gl, nil
}

// resolveProjectPath resolves a project name or identity to a local filesystem
// path. Returns the path and an error if the project is not a local checkout.
func (b *localBackend) resolveProjectPath(ctx context.Context, project string) (string, error) {
	p, err := b.resolveProject(ctx, project)
	if err != nil {
		return "", err
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

func (b *localBackend) Neighbors(ctx context.Context, project, file string) (map[string][]string, error) {
	if err := validateRelativePath(file); err != nil {
		return nil, fmt.Errorf("invalid file path: %w", err)
	}

	p, gl, err := b.graphProject(ctx, project)
	if err != nil {
		return nil, err
	}

	graph, err := gl.FetchGraphNeighbors(ctx, p.ID)
	if err != nil {
		return nil, err
	}

	imports := graph[file]
	var exports []string
	for k, v := range graph {
		if k == file {
			continue
		}
		for _, dep := range v {
			if dep == file {
				exports = append(exports, k)
				break
			}
		}
	}

	return map[string][]string{
		"imports": imports,
		"exports": exports,
	}, nil
}

func (b *localBackend) Trace(ctx context.Context, project string, files []string, maxDepth int) (map[string]int, error) {
	for _, file := range files {
		if err := validateRelativePath(file); err != nil {
			return nil, fmt.Errorf("invalid file path %q: %w", file, err)
		}
	}

	p, gl, err := b.graphProject(ctx, project)
	if err != nil {
		return nil, err
	}

	return gl.FetchGraphPathsBFS(ctx, p.ID, files, maxDepth)
}

func (b *localBackend) Symbols(ctx context.Context, project, file string) ([]analyzer.Symbol, error) {
	if err := validateRelativePath(file); err != nil {
		return nil, fmt.Errorf("invalid file path: %w", err)
	}

	p, err := b.resolveProject(ctx, project)
	if err != nil {
		return nil, err
	}

	cleanProjPath := filepath.Clean(p.Path)
	cleanFullPath := filepath.Clean(filepath.Join(p.Path, file))
	if !strings.HasPrefix(cleanFullPath, cleanProjPath) {
		return nil, fmt.Errorf("path traversal detected: file %q is outside project root %q", file, p.Path)
	}

	content, err := os.ReadFile(cleanFullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", file, err)
	}

	syms := analyzer.Symbols(file, content)
	if syms == nil {
		return []analyzer.Symbol{}, nil
	}
	return syms, nil
}

func validateRelativePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("path is required")
	}
	if filepath.IsAbs(path) {
		return errors.New("path must be relative")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == ".." {
		return errors.New("path is required")
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return errors.New("path must not contain parent directory segments")
		}
	}
	return nil
}

// Callers resolves the project and returns reverse importers of the symbol's file.
func (b *localBackend) Callers(ctx context.Context, project, file string, line int) (*codeintel.CallersResult, error) {
	proj, err := codeintel.ResolveProject(ctx, b.idx, project)
	if err != nil {
		return nil, err
	}
	return codeintel.Callers(ctx, b.idx, proj, codeintel.FileLine{File: file, Line: line})
}

// Explain resolves the project and returns structured symbol context.
func (b *localBackend) Explain(ctx context.Context, project, file string, line int) (*codeintel.ExplainResult, error) {
	proj, err := codeintel.ResolveProject(ctx, b.idx, project)
	if err != nil {
		return nil, err
	}
	return codeintel.Explain(ctx, b.idx, proj, codeintel.FileLine{File: file, Line: line})
}

// Impact resolves the project and returns the reverse-dependency blast radius.
func (b *localBackend) Impact(ctx context.Context, project, file string, line int, depth int) (*codeintel.ImpactResult, error) {
	proj, err := codeintel.ResolveProject(ctx, b.idx, project)
	if err != nil {
		return nil, err
	}
	return codeintel.Impact(ctx, b.idx, proj, codeintel.FileLine{File: file, Line: line}, depth)
}

// DeadCode resolves the project and reports unused symbols.
func (b *localBackend) DeadCode(ctx context.Context, project string) (*codeintel.DeadCodeResult, error) {
	proj, err := codeintel.ResolveProject(ctx, b.idx, project)
	if err != nil {
		return nil, err
	}
	return codeintel.DeadCode(ctx, b.idx, proj)
}

// Diff parses ref_range and runs a semantic symbol diff against the process CWD.
func (b *localBackend) Diff(_ context.Context, refRange string) (*codeintel.DiffResult, error) {
	ref1, ref2, threeDot, err := codeintel.ParseRefRange(refRange)
	if err != nil {
		return nil, err
	}
	return codeintel.Diff("", ref1, ref2, threeDot)
}

// Compile-time check: *localBackend satisfies GitBackend, MultiSearchBackend and GraphBackend.
var _ GitBackend = (*localBackend)(nil)
var _ MultiSearchBackend = (*localBackend)(nil)
var _ GraphBackend = (*localBackend)(nil)
