package agent

import "context"

// SearchScope binds an agent turn to a project (and, for git projects, its
// identity and worktree). It is the contract that keeps tool calls — above all
// semantic_search — inside the intended project regardless of what the model
// passes: the caller sets the scope, the tools enforce it. Without a scope the
// tools fall back to their own argument/auto-detection (standalone CLI/MCP).
//
// (Distinct from Scope in resolver.go, which is the resolved identity/path/source
// of a project for the repo tools.)
type SearchScope struct {
	Project  string // project name the chat is bound to
	Identity string // git repo identity (preferred by search when set)
	Worktree string // worktree toplevel, for git worktree filtering
}

// IsZero reports whether the scope binds nothing.
func (s SearchScope) IsZero() bool {
	return s.Project == "" && s.Identity == "" && s.Worktree == ""
}

// Matches reports whether a project reference supplied by the model refers to the
// bound scope (by name or identity), so a matching explicit argument is allowed.
func (s SearchScope) Matches(ref string) bool {
	return ref == s.Project || (s.Identity != "" && ref == s.Identity)
}

// Label returns a human-readable name for the bound scope (for error messages).
func (s SearchScope) Label() string {
	if s.Project != "" {
		return s.Project
	}
	return s.Identity
}

type scopeKey struct{}

// ContextWithScope returns a context carrying the turn's project scope. Tools
// invoked during the agent loop read it via scopeFromContext.
func ContextWithScope(ctx context.Context, s SearchScope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// scopeFromContext returns the bound scope, if any.
func scopeFromContext(ctx context.Context) (SearchScope, bool) {
	s, ok := ctx.Value(scopeKey{}).(SearchScope)
	return s, ok
}
