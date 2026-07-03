// Package gitmeta resolves a git repository's stable identity and the working
// tree (worktree) a path belongs to. A project indexed from any worktree or
// clone of the same repo shares one identity, so its index is not duplicated
// per checkout; the worktree root is used to resolve result paths back to the
// caller's checkout.
package gitmeta

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitenv"
)

// Info describes the git context of a directory.
type Info struct {
	IsGit    bool
	Toplevel string // absolute path of the current worktree root
	Identity string // stable key shared by all worktrees/clones of the repo
}

// Resolve inspects dir and returns its git Info. For a non-git directory it
// returns Info{IsGit: false}. Identity is the normalized origin remote when one
// exists (so clones over https and ssh collapse to one key), otherwise the
// repository's common git dir (which all local worktrees of a clone share).
func Resolve(ctx context.Context, dir string) Info {
	top, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return Info{}
	}
	info := Info{IsGit: true, Toplevel: top}

	if remote, err := run(ctx, dir, "config", "--get", "remote.origin.url"); err == nil && remote != "" {
		info.Identity = "remote:" + NormalizeRemote(remote)
		return info
	}
	// No remote (a local-only repo): all worktrees share the common git dir.
	if common, err := run(ctx, dir, "rev-parse", "--git-common-dir"); err == nil && common != "" {
		info.Identity = "local:" + common
	} else {
		info.Identity = "local:" + top
	}
	return info
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

// run executes a git subcommand in dir with hermetic config (no global/system
// config, so tests and odd environments behave predictably) and returns trimmed
// stdout.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	// #nosec G204 -- fixed "git" executable; args are literal subcommands and a caller dir.
	cmd := exec.CommandContext(ctx, "git", full...)
	// Strip any inherited GIT_DIR/GIT_WORK_TREE so `git -C dir` resolves the repo
	// from dir (not an ambient repo leaked by a hook or bare-repo worktree).
	// os.DevNull is "/dev/null" on Unix and "NUL" on Windows, so hermetic config
	// works cross-platform.
	cmd.Env = append(gitenv.Clean(cmd.Environ()), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
