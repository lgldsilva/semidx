// Package permission provides the approval gate for agent action tools
// (index_worktree, reindex_project, server_repo_sync). fantasy has no built-in
// approval mechanism, so — like Crush's permission.Service — the check lives at
// the point of execution: an action tool asks an Approver before mutating
// anything. The surface chooses the Approver: an interactive y/N prompt in the
// chatrag REPL, allow-all for trusted automation, or deny-all for untrusted /
// non-interactive surfaces (the safe default).
package permission

import "context"

// Request describes an action awaiting authorization.
type Request struct {
	Tool   string // action tool name, e.g. "index_worktree"
	Path   string // filesystem path or project the action affects
	Detail string // human-readable description shown to the approver
}

// Approver authorizes an action before it executes. It returns (true, nil) to
// allow, (false, nil) for a normal denial, or a non-nil error to abort the call
// (e.g. the prompt could not be read).
type Approver func(ctx context.Context, req Request) (bool, error)

// AllowAll approves every request. Use for trusted, non-interactive automation
// that has already opted in to letting the agent execute actions.
func AllowAll(context.Context, Request) (bool, error) { return true, nil }

// DenyAll denies every request. Safe default for surfaces that cannot prompt a
// human (e.g. an MCP server driven by an external LLM).
func DenyAll(context.Context, Request) (bool, error) { return false, nil }
