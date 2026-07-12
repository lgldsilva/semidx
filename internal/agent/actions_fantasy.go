package agent

import (
	"context"
	"fmt"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/permission"
	"github.com/lgldsilva/semidx/internal/store"
	semidxclient "github.com/lgldsilva/semidx/pkg/client"
)

// ActionTools returns the write/action tools as fantasy.AgentTool, gated by
// policy + approver:
//   - PolicyPropose  → never executes; returns a description ("proposed").
//   - PolicyConfirm  → calls approve before executing; denial is reported back.
//   - PolicyExecute  → executes directly (trusted automation).
//
// idx may be nil (local index actions omitted); client may be nil (server sync
// omitted). Action tools are NOT parallel — they mutate state.
func ActionTools(db store.IndexStore, idx *indexing.Indexer, client *semidxclient.Client, policy ActionPolicy, approve permission.Approver) []fantasy.AgentTool {
	var tools []fantasy.AgentTool
	if db != nil && idx != nil {
		tools = append(tools,
			newIndexWorktreeToolF(db, idx, policy, approve),
			newReindexProjectToolF(db, idx, policy, approve),
		)
	}
	if client != nil {
		tools = append(tools, newServerRepoSyncToolF(client, policy, approve))
	}
	return tools
}

// applyActionPolicy runs the policy gate before an action executes. proceed is
// true only when the action should run; otherwise resp holds the response to
// return (a proposal, a denial, or an approver error). proposal is the base
// result map describing the action.
func applyActionPolicy(ctx context.Context, policy ActionPolicy, approve permission.Approver, req permission.Request, proposal map[string]any) (bool, fantasy.ToolResponse) {
	switch policy {
	case PolicyPropose:
		proposal["proposed"] = true
		return false, fantasy.NewTextResponse(JSONResult(proposal))
	case PolicyConfirm:
		if approve == nil {
			return false, fantasy.NewTextErrorResponse(fmt.Sprintf("%s requires approval but none is configured", req.Tool))
		}
		ok, err := approve(ctx, req)
		if err != nil {
			return false, fantasy.NewTextErrorResponse(fmt.Sprintf("approval error: %v", err))
		}
		if !ok {
			proposal["approved"] = false
			proposal["message"] = "denied by user"
			return false, fantasy.NewTextResponse(JSONResult(proposal))
		}
		return true, fantasy.ToolResponse{}
	case PolicyExecute:
		return true, fantasy.ToolResponse{}
	default:
		return false, fantasy.NewTextErrorResponse("unknown action policy")
	}
}

// --- index_worktree ---

func newIndexWorktreeToolF(db store.IndexStore, idx *indexing.Indexer, policy ActionPolicy, approve permission.Approver) fantasy.AgentTool {
	return fantasy.NewAgentTool("index_worktree",
		"Index a local worktree or directory into the semantic index. Chunks files, embeds them, and makes them searchable. Only a registered project (or a path inside its tree) may be indexed.",
		func(ctx context.Context, in indexWorktreeArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			p, absPath, model, err := resolveRegisteredPath(ctx, db, in)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			proceed, resp := applyActionPolicy(ctx, policy, approve,
				permission.Request{Tool: "index_worktree", Path: absPath, Detail: fmt.Sprintf("index project %q at %s", p.Name, absPath)},
				map[string]any{"action": "index", "project": p.Name, "path": absPath, "model": model})
			if !proceed {
				return resp, nil
			}
			stats, err := idx.IndexProject(ctx, p.ID, absPath, model, 0)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("index failed: %v", err)), nil
			}
			return fantasy.NewTextResponse(JSONResult(map[string]any{
				"action": "index", "project": p.Name, "path": absPath,
				"files_scanned": stats.FilesScanned, "files_indexed": stats.FilesIndexed,
				"files_skipped": stats.FilesSkipped, "chunks": stats.ChunksCreated,
				"errors": stats.Errors, "files_encrypted": stats.FilesEncrypted,
			})), nil
		})
}

// --- reindex_project ---

type reindexArgs struct {
	Project string `json:"project" description:"Project name or identity (must already be registered)"`
	Model   string `json:"model,omitempty" description:"Embedding model (optional; defaults to the project's model)"`
}

func newReindexProjectToolF(db store.IndexStore, idx *indexing.Indexer, policy ActionPolicy, approve permission.Approver) fantasy.AgentTool {
	return fantasy.NewAgentTool("reindex_project",
		"Re-index an already-registered project, refreshing chunks and embeddings from its source path. Use index_worktree for new paths.",
		func(ctx context.Context, in reindexArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			p, err := db.GetProject(ctx, in.Project)
			if err != nil {
				p, err = db.GetProjectByIdentity(ctx, in.Project)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("project %q not found — register it first with index_worktree or `semidx index`", in.Project)), nil
				}
			}
			if p.Path == "" {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("project %q has no local path (remote-only)", in.Project)), nil
			}
			model := in.Model
			if model == "" {
				model = p.Model
			}
			proceed, resp := applyActionPolicy(ctx, policy, approve,
				permission.Request{Tool: "reindex_project", Path: p.Path, Detail: fmt.Sprintf("reindex project %q", p.Name)},
				map[string]any{"action": "reindex", "project": p.Name, "path": p.Path, "model": model})
			if !proceed {
				return resp, nil
			}
			stats, err := idx.IndexProject(ctx, p.ID, p.Path, model, 0)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("reindex failed: %v", err)), nil
			}
			return fantasy.NewTextResponse(JSONResult(map[string]any{
				"action": "reindex", "project": p.Name, "path": p.Path,
				"files_scanned": stats.FilesScanned, "files_indexed": stats.FilesIndexed,
				"files_skipped": stats.FilesSkipped, "chunks": stats.ChunksCreated, "errors": stats.Errors,
			})), nil
		})
}

// --- server_repo_sync ---

type serverSyncArgs struct {
	Project string `json:"project" description:"Project name registered on the server"`
	Branch  string `json:"branch,omitempty" description:"Git branch to sync (optional; defaults to the project's configured branch)"`
}

func newServerRepoSyncToolF(client *semidxclient.Client, policy ActionPolicy, approve permission.Approver) fantasy.AgentTool {
	return fantasy.NewAgentTool("server_repo_sync",
		"Trigger a server-side git repository sync (clone or pull) and reindex job for a project registered on the remote server. Only available when logged in to a semidx server.",
		func(ctx context.Context, in serverSyncArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if client == nil {
				return fantasy.NewTextErrorResponse("not logged in to a remote server — use `semidx login <url>` first, or index locally"), nil
			}
			proj, err := client.GetProject(ctx, in.Project)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("project %q not found on server: %v", in.Project, err)), nil
			}
			proceed, resp := applyActionPolicy(ctx, policy, approve,
				permission.Request{Tool: "server_repo_sync", Path: proj.Name, Detail: fmt.Sprintf("server sync+reindex %q", proj.Name)},
				map[string]any{"action": "sync", "project": proj.Name, "git_url": proj.GitURL, "branch": in.Branch})
			if !proceed {
				return resp, nil
			}
			jobID, err := client.EnqueueJob(ctx, in.Project, "full")
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("enqueue sync job failed: %v", err)), nil
			}
			return fantasy.NewTextResponse(JSONResult(map[string]any{
				"action": "sync", "project": proj.Name, "job_id": jobID, "job_type": "full",
				"message": fmt.Sprintf("Queued full sync+reindex job #%d for %q", jobID, proj.Name),
			})), nil
		})
}
