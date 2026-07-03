// Package gitsync clones or updates a git repository into the server's data
// directory so the server can index projects it owns, without clients uploading
// anything.
package gitsync

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Sync ensures dataDir/repos/<name> holds an up-to-date checkout of url and
// returns its path. It clones (shallow) on first use and fast-forward pulls
// afterwards. Only https:// and git@ (SSH) URLs are accepted.
func Sync(ctx context.Context, dataDir, name, url, branch string) (string, error) {
	if !validURL(url) {
		return "", fmt.Errorf("unsupported git url %q (want https:// or git@)", url)
	}
	repoPath := filepath.Join(dataDir, "repos", name)

	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
		if err := run(ctx, repoPath, "pull", "--ff-only"); err != nil {
			return "", fmt.Errorf("git pull: %w", err)
		}
		return repoPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(repoPath), 0o750); err != nil {
		return "", err
	}
	args := []string{"clone", "--depth", "50"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, url, repoPath)
	if err := run(ctx, "", args...); err != nil {
		return "", fmt.Errorf("git clone: %w", err)
	}
	return repoPath, nil
}

func validURL(url string) bool {
	// https:// and git@ (SSH) for remotes; file:// for a local mirror on the same
	// host (the admin controls the URL on a self-hosted server).
	return strings.HasPrefix(url, "https://") ||
		strings.HasPrefix(url, "git@") ||
		strings.HasPrefix(url, "file://")
}

func run(ctx context.Context, dir string, args ...string) error {
	// #nosec G204 -- the executable is the fixed literal "git"; the URL is
	// validated by validURL and the rest of the args are built by this package.
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd = exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- see above
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
