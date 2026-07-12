// Package repotools provides read-only git workspace tools — listing worktrees,
// branches, and repo status — using the shared gitexec.Run helper. These tools
// power the new 'semidx repo worktrees/branches/info' CLI commands and the
// agent's git tools.
package repotools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitexec"
)

const refsHeadsPrefix = "refs/heads/"

// Worktree is one row of `git worktree list --porcelain`.
type Worktree struct {
	Path   string
	HEAD   string // short SHA
	Branch string // ref name; empty for detached HEAD
	Bare   bool
}

// Branch is one branch from `git for-each-ref`.
type Branch struct {
	Name     string
	FullRef  string // e.g. "refs/heads/main"
	Remote   bool
	Current  bool
	Tracking string // upstream, e.g. "origin/main"
	Ahead    int
	Behind   int
}

// RepoStatus summarises the working tree state.
type RepoStatus struct {
	CurrentBranch string
	Detached      bool
	Dirty         bool
	HEAD          string // short SHA
}

// ListWorktrees returns all worktrees of the repository at root.
// Uses `git worktree list --porcelain` (machine-parseable output).
func ListWorktrees(ctx context.Context, root string) ([]Worktree, error) {
	out, err := gitexec.Run(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	lines := strings.Split(out, "\n")
	return parseWorktreePorcelain(lines), nil
}

// parseWorktreePorcelain parses the --porcelain output lines.
//
// Blocks are separated by blank lines. Each block begins with a "worktree <path>"
// line, followed by optional "HEAD <sha>", "branch <ref>", and "bare" lines.
func parseWorktreePorcelain(lines []string) []Worktree {
	var worktrees []Worktree
	var current *Worktree

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			current = flushWorktree(current, &worktrees)
			continue
		}
		current = parseWorktreeLine(line, current, &worktrees)
	}

	// Flush the last block if it had no trailing blank line.
	return appendWorktree(current, worktrees)
}

// flushWorktree saves the current worktree on a blank line.
func flushWorktree(cur *Worktree, acc *[]Worktree) *Worktree {
	if cur != nil {
		*acc = append(*acc, *cur)
	}
	return nil
}

// parseWorktreeLine dispatches one porcelain line to the current worktree.
func parseWorktreeLine(line string, cur *Worktree, acc *[]Worktree) *Worktree {
	switch {
	case strings.HasPrefix(line, "worktree "):
		return startWorktree(line, cur, acc)
	case strings.HasPrefix(line, "HEAD "):
		if cur != nil {
			cur.HEAD = trimHEAD(line)
		}
	case strings.HasPrefix(line, "branch "):
		if cur != nil {
			cur.Branch = trimBranchRef(line)
		}
	case line == "bare":
		if cur != nil {
			cur.Bare = true
		}
	}
	return cur
}

// startWorktree creates a new worktree from a "worktree <path>" line.
func startWorktree(line string, cur *Worktree, acc *[]Worktree) *Worktree {
	if cur != nil {
		*acc = append(*acc, *cur)
	}
	return &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
}

// trimHEAD extracts the short SHA from a "HEAD <sha>" line.
func trimHEAD(line string) string {
	sha := strings.TrimPrefix(line, "HEAD ")
	if len(sha) > 7 {
		sha = sha[:7]
	}
	return sha
}

// trimBranchRef extracts the branch name from a "branch <ref>" line.
func trimBranchRef(line string) string {
	return strings.TrimPrefix(strings.TrimPrefix(line, "branch "), refsHeadsPrefix)
}

// appendWorktree adds the final worktree if no trailing blank line.
func appendWorktree(cur *Worktree, acc []Worktree) []Worktree {
	if cur != nil {
		acc = append(acc, *cur)
	}
	return acc
}

// ListBranches returns local (and optionally remote) branches.
// Uses `git for-each-ref --format=%(refname)%09%(upstream:short)%09%(upstream:track)`.
// This is locale/version-stable, unlike `git branch -vv`.
func ListBranches(ctx context.Context, root string, includeRemote bool) ([]Branch, error) {
	lines, err := executeForEachRef(ctx, root, includeRemote)
	if err != nil {
		return nil, err
	}

	branches := parseForEachRef(lines)

	// Determine which branch is currently checked out.
	if current, err := gitexec.Run(ctx, root, "symbolic-ref", "--short", "HEAD"); err == nil && current != "" {
		for i := range branches {
			if branches[i].Name == current {
				branches[i].Current = true
				break
			}
		}
	}

	// Callers are responsible for sorting; the CLI re-sorts with
	// "current first → local alpha → remote alpha". No sort here.
	return branches, nil
}

// executeForEachRef runs git for-each-ref and returns the output lines.
func executeForEachRef(ctx context.Context, root string, includeRemote bool) ([]string, error) {
	args := []string{
		"for-each-ref",
		"--format=%(refname)%09%(upstream:short)%09%(upstream:track)",
		"refs/heads",
	}
	if includeRemote {
		args = append(args, "refs/remotes")
	}

	out, err := gitexec.Run(ctx, root, args...)
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	return strings.Split(out, "\n"), nil
}

// parseForEachRef parses for-each-ref output lines into Branches.
// Each line is tab-separated: fullRef\tupstreamShort\t[track].
// fullRef is e.g. "refs/heads/main" or "refs/remotes/origin/main".
func parseForEachRef(lines []string) []Branch {
	var branches []Branch

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 1 || parts[0] == "" {
			continue
		}

		fullRef := parts[0]
		branch := Branch{FullRef: fullRef}

		switch {
		case strings.HasPrefix(fullRef, refsHeadsPrefix):
			branch.Name = strings.TrimPrefix(fullRef, refsHeadsPrefix)
			branch.Remote = false
		case strings.HasPrefix(fullRef, "refs/remotes/"):
			branch.Name = strings.TrimPrefix(fullRef, "refs/remotes/")
			branch.Remote = true
		default:
			// Fallback: use the full ref as-is (shouldn't happen).
			branch.Name = fullRef
			branch.Remote = strings.Contains(fullRef, "/")
		}

		// Second column: upstream short name (e.g. "origin/main").
		if len(parts) > 1 && parts[1] != "" {
			branch.Tracking = parts[1]
		}

		// Third column: tracking info like "[ahead 1, behind 0]".
		if len(parts) > 2 && parts[2] != "" {
			parseTrackingInfo(&branch, parts[2])
		}

		branches = append(branches, branch)
	}

	return branches
}

// parseTrackingInfo parses the output of %(upstream:track) (e.g. "[ahead 1, behind 0]")
// and updates Ahead/Behind on the Branch.
func parseTrackingInfo(b *Branch, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	// Strip surrounding brackets.
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")

	if raw == "gone" {
		return
	}

	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "ahead "):
			if n, err := strconv.Atoi(strings.TrimPrefix(part, "ahead ")); err == nil {
				b.Ahead = n
			}
		case strings.HasPrefix(part, "behind "):
			if n, err := strconv.Atoi(strings.TrimPrefix(part, "behind ")); err == nil {
				b.Behind = n
			}
		}
	}
}

// Status returns the repo's current working tree state.
// Uses `git status --porcelain=v1` + `git rev-parse --short HEAD` + `git branch --show-current`.
func Status(ctx context.Context, root string) (*RepoStatus, error) {
	// --porcelain=v1 outputs nothing when the working tree is clean.
	statusOut, err := gitexec.Run(ctx, root, "status", "--porcelain=v1")
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}

	head, err := gitexec.Run(ctx, root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git rev-parse: %w", err)
	}

	// git branch --show-current returns empty for detached HEAD.
	branch, err := gitexec.Run(ctx, root, "branch", "--show-current")
	if err != nil {
		return nil, fmt.Errorf("git branch --show-current: %w", err)
	}

	return &RepoStatus{
		CurrentBranch: branch,
		Detached:      branch == "",
		Dirty:         statusOut != "",
		HEAD:          head,
	}, nil
}
