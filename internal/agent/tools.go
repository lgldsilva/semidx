package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/repotools"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// NewSearchTool creates a semantic_search tool for the agent.
func NewSearchTool(svc *search.Service) Tool {
	return &searchTool{svc: svc}
}

type searchTool struct {
	svc *search.Service
}

func (t *searchTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "semantic_search",
		Description: "Search indexed code and documents by meaning. Returns file:line results with relevance scores. Use for intent/behavior queries, not exact string matching.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Natural language query describing what you're looking for",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or identity (default: auto-detect from workspace)",
				},
				"top_k": map[string]any{
					"type": "integer", "default": 5,
					"description": "Number of results to return",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t *searchTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Query   string `json:"query"`
		Project string `json:"project,omitempty"`
		TopK    int    `json:"top_k,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.TopK <= 0 {
		args.TopK = 5
	}

	resp, err := t.svc.Search(ctx, search.Request{
		Project: args.Project,
		Query:   args.Query,
		TopK:    args.TopK,
	})
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	type hit struct {
		File      string  `json:"file"`
		StartLine int     `json:"start_line"`
		EndLine   int     `json:"end_line"`
		Score     float64 `json:"score"`
		Content   string  `json:"content"`
	}
	var hits []hit
	for _, r := range resp.Results {
		hits = append(hits, hit{
			File: r.FilePath, StartLine: r.StartLine, EndLine: r.EndLine,
			Score: r.Score, Content: truncate(r.Content, 200),
		})
	}
	if hits == nil {
		hits = []hit{}
	}
	return JSONResult(map[string]any{
		"results": hits,
		"total":   len(hits),
		"project": resp.Project.Name,
		"keyword": resp.Keyword,
	}), nil
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// NewIndexStatusTool creates an index_status tool.
func NewIndexStatusTool(db store.IndexStore) Tool {
	return &indexStatusTool{db: db}
}

type indexStatusTool struct {
	db store.IndexStore
}

func (t *indexStatusTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "index_status",
		Description: "Check whether a project is indexed, how many files, and what model. Use before searching if unsure about indexing status.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or path (default: current directory)",
				},
			},
			"required": []string{},
		},
	}
}

func (t *indexStatusTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Project string `json:"project,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	project := args.Project
	if project == "" {
		return JSONResult(map[string]string{
			"error": `no project specified; call semantic_projects first to list available projects`,
		}), nil
	}

	p, err := t.db.GetProject(ctx, project)
	if err != nil {
		p, err = t.db.GetProjectByIdentity(ctx, project)
		if err != nil {
			return "", fmt.Errorf("project %q not found", project)
		}
	}

	hashes, err := t.db.ListFileHashes(ctx, p.ID)
	count := 0
	if err == nil {
		count = len(hashes)
	}

	return JSONResult(map[string]any{
		"name":   p.Name,
		"status": p.Status,
		"model":  p.Model,
		"source": p.SourceType,
		"files":  count,
	}), nil
}

// ---------------------------------------------------------------------------
// repo_worktrees tool
// ---------------------------------------------------------------------------

// NewRepoWorktreesTool creates a tool that lists git worktrees.
// Takes a ScopeResolver to resolve project references to filesystem paths.
func NewRepoWorktreesTool(resolver ScopeResolver) Tool {
	return &repoWorktreesTool{resolver: resolver}
}

type repoWorktreesTool struct{ resolver ScopeResolver }

func (t *repoWorktreesTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "repo_worktrees",
		Description: "List git worktrees (linked working trees) for a project. Returns path, HEAD commit, branch, and bare flag for each worktree.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or identity (default: auto-detect from workspace)",
				},
			},
			"required": []string{},
		},
	}
}

func (t *repoWorktreesTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Project string `json:"project,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	root, err := resolveRoot(ctx, t.resolver, args.Project)
	if err != nil {
		return "", err
	}

	wts, err := repotools.ListWorktrees(ctx, root)
	if err != nil {
		return "", fmt.Errorf("list worktrees failed: %w", err)
	}

	type wt struct {
		Path   string `json:"path"`
		HEAD   string `json:"head"`
		Branch string `json:"branch"`
		Bare   bool   `json:"bare"`
	}
	results := make([]wt, len(wts))
	for i, w := range wts {
		results[i] = wt{Path: w.Path, HEAD: w.HEAD, Branch: w.Branch, Bare: w.Bare}
	}
	if results == nil {
		results = []wt{}
	}
	return JSONResult(map[string]any{
		"worktrees": results,
		"total":     len(results),
	}), nil
}

// ---------------------------------------------------------------------------
// repo_branches tool
// ---------------------------------------------------------------------------

// NewRepoBranchesTool creates a tool that lists git branches.
func NewRepoBranchesTool(resolver ScopeResolver) Tool {
	return &repoBranchesTool{resolver: resolver}
}

type repoBranchesTool struct{ resolver ScopeResolver }

func (t *repoBranchesTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "repo_branches",
		Description: "List git branches for a project. Shows local branches by default; include remote branches with remote=true. Each branch shows ahead/behind tracking info.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or identity (default: auto-detect from workspace)",
				},
				"remote": map[string]any{
					"type":        "boolean",
					"description": "Include remote branches",
					"default":     false,
				},
			},
			"required": []string{},
		},
	}
}

func (t *repoBranchesTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Project string `json:"project,omitempty"`
		Remote  bool   `json:"remote,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	root, err := resolveRoot(ctx, t.resolver, args.Project)
	if err != nil {
		return "", err
	}

	branches, err := repotools.ListBranches(ctx, root, args.Remote)
	if err != nil {
		return "", fmt.Errorf("list branches failed: %w", err)
	}

	type br struct {
		Name    string `json:"name"`
		Current bool   `json:"current"`
		Remote  bool   `json:"remote"`
		Ahead   int    `json:"ahead"`
		Behind  int    `json:"behind"`
	}
	results := make([]br, len(branches))
	for i, b := range branches {
		results[i] = br{Name: b.Name, Current: b.Current, Remote: b.Remote, Ahead: b.Ahead, Behind: b.Behind}
	}
	if results == nil {
		results = []br{}
	}
	return JSONResult(map[string]any{
		"branches": results,
		"total":    len(results),
	}), nil
}

// ---------------------------------------------------------------------------
// repo_status tool
// ---------------------------------------------------------------------------

// NewRepoStatusTool creates a tool that returns git repository status.
func NewRepoStatusTool(resolver ScopeResolver) Tool {
	return &repoStatusTool{resolver: resolver}
}

type repoStatusTool struct{ resolver ScopeResolver }

func (t *repoStatusTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "repo_status",
		Description: "Check git repository status: current branch, dirty state, detached HEAD. Useful before making changes or understanding what branch is active.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or identity (default: auto-detect from workspace)",
				},
			},
			"required": []string{},
		},
	}
}

func (t *repoStatusTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Project string `json:"project,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	root, err := resolveRoot(ctx, t.resolver, args.Project)
	if err != nil {
		return "", err
	}

	s, err := repotools.Status(ctx, root)
	if err != nil {
		return "", fmt.Errorf("status failed: %w", err)
	}

	return JSONResult(map[string]any{
		"current_branch": s.CurrentBranch,
		"detached":       s.Detached,
		"dirty":          s.Dirty,
		"head":           s.HEAD,
	}), nil
}

// ---------------------------------------------------------------------------
// list_projects tool
// ---------------------------------------------------------------------------

// NewListProjectsTool creates a tool that lists indexed projects.
func NewListProjectsTool(db store.IndexStore) Tool {
	return &listProjectsTool{db: db}
}

type listProjectsTool struct{ db store.IndexStore }

func (t *listProjectsTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "list_projects",
		Description: "List all indexed projects with their name, identity, source type, indexing status, and embedding model. Call this first when you need to discover what is available.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *listProjectsTool) Run(ctx context.Context, argsJSON string) (string, error) {
	projects, err := t.db.ListProjects(ctx, 0, 0)
	if err != nil {
		return "", fmt.Errorf("list projects failed: %w", err)
	}

	type proj struct {
		Name       string `json:"name"`
		Identity   string `json:"identity"`
		SourceType string `json:"source_type"`
		Status     string `json:"status"`
		Model      string `json:"model"`
	}
	results := make([]proj, len(projects))
	for i, p := range projects {
		results[i] = proj{
			Name: p.Name, Identity: p.Identity,
			SourceType: p.SourceType, Status: p.Status, Model: p.Model,
		}
	}
	if results == nil {
		results = []proj{}
	}
	return JSONResult(map[string]any{
		"projects": results,
		"total":    len(results),
	}), nil
}

// resolveRoot resolves a project reference to a filesystem root path.
func resolveRoot(ctx context.Context, resolver ScopeResolver, ref string) (string, error) {
	if resolver == nil {
		return "", fmt.Errorf("scope resolver not available — cannot resolve project path")
	}
	if ref == "" {
		return "", fmt.Errorf("no project specified; provide a project argument")
	}
	scope, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("resolve project %q: %w", ref, err)
	}
	if scope.Path == "" {
		return "", fmt.Errorf("project %q has no local path (remote-only)", ref)
	}
	return scope.Path, nil
}
