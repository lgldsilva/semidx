package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
	semidxclient "github.com/lgldsilva/semidx/pkg/client"
)

const errInvalidArgs = "invalid arguments: %w"

// ---------------------------------------------------------------------------
// index_worktree tool
// ---------------------------------------------------------------------------

// NewIndexWorktreeTool creates an action tool that indexes a local path/worktree.
// The policy controls whether the tool proposes, confirms, or executes.
func NewIndexWorktreeTool(db store.IndexStore, idx *indexing.Indexer, policy ActionPolicy) Tool {
	return &indexWorktreeTool{db: db, idx: idx, policy: policy}
}

type indexWorktreeTool struct {
	db     store.IndexStore
	idx    *indexing.Indexer
	policy ActionPolicy
}

func (t *indexWorktreeTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "index_worktree",
		Description: "Index a local worktree or directory into the semantic index. Chunks files, embeds them, and makes them searchable. Use when you need a new project indexed or a worktree refreshed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or filesystem path to index",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Explicit filesystem path (optional; defaults to resolved project path)",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Embedding model name (optional; defaults to project or configured model)",
				},
			},
			"required": []string{"project"},
		},
	}
}

// indexWorktreeArgs holds parsed arguments for the index_worktree tool.
type indexWorktreeArgs struct {
	Project string `json:"project"`
	Path    string `json:"path,omitempty"`
	Model   string `json:"model,omitempty"`
}

// parseAndValidateIndexArgs unmarshals and validates the JSON arguments string.
func parseAndValidateIndexArgs(argsJSON string) (indexWorktreeArgs, error) {
	var args indexWorktreeArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return args, fmt.Errorf(errInvalidArgs, err)
	}
	return args, nil
}

// resolveIndexPath resolves the project and filesystem path from the arguments.
//
// Security: this tool is reachable by an LLM (potentially via an external MCP
// client), so it must NEVER index an arbitrary filesystem path. The project
// must already be registered, and an explicit path is only honored when it
// stays inside that project's tree (its own path or a subdirectory/worktree of
// it). Anything else is rejected. Returns the project, the absolute path, and
// the embedding model.
func (t *indexWorktreeTool) resolveIndexPath(ctx context.Context, args indexWorktreeArgs) (*store.Project, string, string, error) {
	p, err := t.db.GetProject(ctx, args.Project)
	if err != nil {
		p, err = t.db.GetProjectByIdentity(ctx, args.Project)
	}
	if err != nil || p == nil {
		return nil, "", "", fmt.Errorf("project not found: %q (register it first; arbitrary paths are not allowed)", args.Project)
	}
	if p.Path == "" {
		return nil, "", "", fmt.Errorf("project %q has no local path (remote-only)", args.Project)
	}

	base, err := filepath.Abs(p.Path)
	if err != nil {
		return nil, "", "", fmt.Errorf("resolving project path %q: %w", p.Path, err)
	}

	// Default to the project's own path. An explicit path is accepted only if
	// it resolves inside the project's tree — this is what keeps an LLM from
	// pointing the indexer at /etc, ~/.ssh, or any path outside the project.
	targetPath := base
	if args.Path != "" {
		abs, err := filepath.Abs(args.Path)
		if err != nil {
			return nil, "", "", fmt.Errorf("resolving path %q: %w", args.Path, err)
		}
		rel, err := filepath.Rel(base, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, "", "", fmt.Errorf("path %q is outside registered project %q", abs, p.Name)
		}
		targetPath = abs
	}

	model := args.Model
	if model == "" {
		model = p.Model
	}
	return p, targetPath, model, nil
}

func (t *indexWorktreeTool) Run(ctx context.Context, argsJSON string) (string, error) {
	args, err := parseAndValidateIndexArgs(argsJSON)
	if err != nil {
		return "", err
	}

	p, absPath, model, err := t.resolveIndexPath(ctx, args)
	if err != nil {
		// Resolution failures (unknown project, path outside the project) are
		// reported back to the model as a soft error, not a hard failure.
		return JSONResult(map[string]any{
			"action":   "index",
			"error":    err.Error(),
			"proposed": false,
		}), nil
	}

	// Policy: propose or confirm — describe what would be done.
	if t.policy == PolicyPropose || t.policy == PolicyConfirm {
		result := map[string]any{
			"action":   "index",
			"project":  args.Project,
			"path":     absPath,
			"model":    model,
			"proposed": true,
		}
		if t.policy == PolicyConfirm {
			result["confirm_required"] = true
		}
		return JSONResult(result), nil
	}

	// Policy: execute — run the indexer.
	return t.indexProjectWithPolicy(ctx, args.Project, p, absPath, model)
}

// indexProjectWithPolicy executes the indexer and returns the JSON result.
func (t *indexWorktreeTool) indexProjectWithPolicy(ctx context.Context, projectName string, p *store.Project, absPath, model string) (string, error) {
	projectID := 0
	if p != nil {
		projectID = p.ID
	}
	stats, err := t.idx.IndexProject(ctx, projectID, absPath, model, 0)
	if err != nil {
		return "", fmt.Errorf("index failed: %w", err)
	}
	return JSONResult(map[string]any{
		"action":          "index",
		"project":         projectName,
		"path":            absPath,
		"files_scanned":   stats.FilesScanned,
		"files_indexed":   stats.FilesIndexed,
		"files_skipped":   stats.FilesSkipped,
		"chunks":          stats.ChunksCreated,
		"errors":          stats.Errors,
		"files_encrypted": stats.FilesEncrypted,
	}), nil
}

// ---------------------------------------------------------------------------
// reindex_project tool
// ---------------------------------------------------------------------------

// NewReindexProjectTool creates an action tool that reindexes an existing project.
func NewReindexProjectTool(db store.IndexStore, idx *indexing.Indexer, policy ActionPolicy) Tool {
	return &reindexProjectTool{db: db, idx: idx, policy: policy}
}

type reindexProjectTool struct {
	db     store.IndexStore
	idx    *indexing.Indexer
	policy ActionPolicy
}

func (t *reindexProjectTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "reindex_project",
		Description: "Re-index an already-registered project, refreshing chunks and embeddings from its source path. Only registered projects are accepted; use index_worktree for new paths.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or identity (must already be registered in the index)",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Embedding model name (optional; defaults to the project's stored model)",
				},
			},
			"required": []string{"project"},
		},
	}
}

func (t *reindexProjectTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Project string `json:"project"`
		Model   string `json:"model,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf(errInvalidArgs, err)
	}

	// Resolve the project; must already exist.
	p, err := t.db.GetProject(ctx, args.Project)
	if err != nil {
		p, err = t.db.GetProjectByIdentity(ctx, args.Project)
		if err != nil {
			return JSONResult(map[string]any{
				"action":   "reindex",
				"error":    fmt.Sprintf("project %q not found — register it first with index_worktree or `semidx index`", args.Project),
				"proposed": false,
			}), nil
		}
	}

	if p.Path == "" {
		return JSONResult(map[string]any{
			"action":   "reindex",
			"error":    fmt.Sprintf("project %q has no local path (remote-only)", args.Project),
			"proposed": false,
		}), nil
	}

	model := args.Model
	if model == "" {
		model = p.Model
	}

	// Policy: propose or confirm.
	if t.policy == PolicyPropose || t.policy == PolicyConfirm {
		result := map[string]any{
			"action":   "reindex",
			"project":  p.Name,
			"path":     p.Path,
			"model":    model,
			"proposed": true,
		}
		if t.policy == PolicyConfirm {
			result["confirm_required"] = true
		}
		return JSONResult(result), nil
	}

	// Policy: execute.
	stats, err := t.idx.IndexProject(ctx, p.ID, p.Path, model, 0)
	if err != nil {
		return "", fmt.Errorf("reindex failed: %w", err)
	}
	return JSONResult(map[string]any{
		"action":          "reindex",
		"project":         p.Name,
		"path":            p.Path,
		"files_scanned":   stats.FilesScanned,
		"files_indexed":   stats.FilesIndexed,
		"files_skipped":   stats.FilesSkipped,
		"chunks":          stats.ChunksCreated,
		"errors":          stats.Errors,
		"files_encrypted": stats.FilesEncrypted,
	}), nil
}

// ---------------------------------------------------------------------------
// server_repo_sync tool
// ---------------------------------------------------------------------------

// NewServerRepoSyncTool creates an action tool that triggers a server-side
// git sync (clone/pull) and reindex via the remote API. Requires a logged-in
// client; nil client makes the tool return an error.
func NewServerRepoSyncTool(client *semidxclient.Client, policy ActionPolicy) Tool {
	return &serverRepoSyncTool{client: client, policy: policy}
}

type serverRepoSyncTool struct {
	client *semidxclient.Client
	policy ActionPolicy
}

func (t *serverRepoSyncTool) Def() chat.ToolDef {
	return chat.ToolDef{
		Name:        "server_repo_sync",
		Description: "Trigger a server-side git repository sync (clone or pull) and reindex job for a project registered on the remote server. Only available when logged in to a semidx server.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name registered on the server",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Git branch to sync (optional; defaults to the project's configured branch)",
				},
			},
			"required": []string{"project"},
		},
	}
}

func (t *serverRepoSyncTool) Run(ctx context.Context, argsJSON string) (string, error) {
	if t.client == nil {
		return JSONResult(map[string]any{
			"action":   "sync",
			"error":    "not logged in to a remote server — use `semidx login <url>` first, or run local sync via `semidx index`",
			"proposed": false,
		}), nil
	}

	var args struct {
		Project string `json:"project"`
		Branch  string `json:"branch,omitempty"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf(errInvalidArgs, err)
	}

	// Verify the project exists on the server.
	proj, err := t.client.GetProject(ctx, args.Project)
	if err != nil {
		return JSONResult(map[string]any{
			"action":   "sync",
			"error":    fmt.Sprintf("project %q not found on server: %s", args.Project, err),
			"proposed": false,
		}), nil
	}

	// Policy: propose or confirm.
	if t.policy == PolicyPropose || t.policy == PolicyConfirm {
		result := map[string]any{
			"action":   "sync",
			"project":  proj.Name,
			"git_url":  proj.GitURL,
			"branch":   args.Branch,
			"proposed": true,
		}
		if t.policy == PolicyConfirm {
			result["confirm_required"] = true
		}
		return JSONResult(result), nil
	}

	// Policy: execute — enqueue a full reindex job on the server.
	jobType := "full"
	jobID, err := t.client.EnqueueJob(ctx, args.Project, jobType)
	if err != nil {
		return "", fmt.Errorf("enqueue sync job failed: %s", err)
	}
	return JSONResult(map[string]any{
		"action":   "sync",
		"project":  proj.Name,
		"job_id":   jobID,
		"job_type": jobType,
		"message":  fmt.Sprintf("Queued %s sync+reindex job #%d for project %q", jobType, jobID, proj.Name),
	}), nil
}
