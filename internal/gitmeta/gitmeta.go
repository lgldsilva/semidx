// Package gitmeta resolves a git repository's stable identity and the working
// tree (worktree) a path belongs to. A project indexed from any worktree or
// clone of the same repo shares one identity, so its index is not duplicated
// per checkout; the worktree root is used to resolve result paths back to the
// caller's checkout.
package gitmeta

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitexec"
)

// Info describes the git context of a directory.
type Info struct {
	IsGit    bool
	Toplevel string // absolute path of the current worktree root
	Identity string // stable key shared by all worktrees/clones of the repo
	Origin   string // raw remote.origin.url when the checkout has one
}

// Resolve inspects dir and returns its git Info. For a non-git directory it
// returns Info{IsGit: false}. Identity is the normalized origin remote when one
// exists (so clones over https and ssh collapse to one key), otherwise the
// repository's common git dir (which all local worktrees of a clone share).
func Resolve(ctx context.Context, dir string) Info {
	top, err := gitexec.Run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return Info{}
	}
	info := Info{IsGit: true, Toplevel: top}

	if remote, err := gitexec.Run(ctx, dir, "config", "--get", "remote.origin.url"); err == nil && remote != "" {
		info.Origin = remote
		info.Identity = "remote:" + NormalizeRemote(remote)
		return info
	}
	// No remote (a local-only repo): all worktrees share the common git dir.
	info.Identity = "local:" + localIdentity(ctx, dir, top)
	return info
}

// localIdentity returns the absolute common git dir of a remote-less repo, which
// every worktree of that clone shares. `git rev-parse --git-common-dir` answers
// relative to the current directory (".git" at the repo root, "../.git" one level
// down), so the raw value both collides across unrelated local repos and changes
// with the directory it is asked from; --path-format=absolute (git >= 2.31) fixes
// both. Older git falls back to resolving the relative answer against dir, and a
// repo whose common dir cannot be resolved at all falls back to the worktree root.
func localIdentity(ctx context.Context, dir, top string) string {
	if common, err := gitexec.Run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && filepath.IsAbs(common) {
		return filepath.Clean(common)
	}
	common, err := gitexec.Run(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return top
	}
	return resolveCommonDir(dir, top, common)
}

// resolveCommonDir turns a --git-common-dir answer into an absolute path. The
// answer is relative to dir (".git" at a repo root, "../.git" one level down),
// so it must be joined with dir before it identifies anything; top is the
// fallback for an empty or unresolvable answer.
func resolveCommonDir(dir, top, common string) string {
	if common == "" {
		return top
	}
	if filepath.IsAbs(common) {
		return filepath.Clean(common)
	}
	abs, err := filepath.Abs(filepath.Join(dir, common))
	if err != nil {
		return top
	}
	return filepath.Clean(abs)
}

// NormalizeRemote reduces a git remote URL to a canonical "host/path" key so the
// same repository reached over https, ssh (scp-like git@host:path) or with
// embedded credentials all map to the same identity.
func NormalizeRemote(url string) string {
	s := strings.TrimSpace(url)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	// Strip a scheme (https://, http://, ssh://, git://).
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else if at := strings.Index(s, "@"); at >= 0 && strings.Contains(s, ":") && !strings.Contains(s[:at], "/") {
		// scp-like syntax: git@host:org/repo -> host:org/repo (userinfo dropped below).
		s = s[at+1:]
		// host:org/repo -> host/org/repo (only the first ':' is the host separator).
		if c := strings.Index(s, ":"); c >= 0 {
			s = s[:c] + "/" + s[c+1:]
		}
		return strings.ToLower(s)
	}

	// Drop any remaining userinfo (user:pass@ or user@) from the authority.
	if at := strings.Index(s, "@"); at >= 0 {
		s = s[at+1:]
	}
	return strings.ToLower(s)
}

// CanonicalOrigin is NormalizeRemote plus the leftover scp form that RedactURL
// produces (host:org/repo without user@). Use this when comparing a local git
// identity to a server GitURL; do not persist it as a project identity, so
// URLs with ports keep their existing remote:host:port/path keys.
func CanonicalOrigin(raw string) string {
	normalized := NormalizeRemote(raw)
	colon := strings.IndexByte(normalized, ':')
	slash := strings.IndexByte(normalized, '/')
	if colon > 0 && (slash < 0 || colon < slash) {
		normalized = normalized[:colon] + "/" + normalized[colon+1:]
	}
	return normalized
}
