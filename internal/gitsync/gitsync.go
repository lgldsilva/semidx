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

	"github.com/lgldsilva/semidx/internal/gitenv"
)

// Sync ensures dataDir/repos/<name> holds an up-to-date checkout of url and
// returns its path. It clones (shallow) on first use and fast-forward pulls
// afterwards. Only https:// and git@ (SSH) URLs are accepted by default; file://
// is allowed only when allowFileURL is true (SEMIDX_GIT_ALLOW_FILE).
func Sync(ctx context.Context, dataDir, name, url, branch string, allowFileURL bool) (string, error) {
	if !validURL(url, allowFileURL) {
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

func validURL(url string, allowFile bool) bool {
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "git@") {
		return true
	}
	return allowFile && strings.HasPrefix(url, "file://")
}

func run(ctx context.Context, dir string, args ...string) error {
	// Verify the executable resolves to a real binary.
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found: %w", err)
	}
	var cmdArgs []string
	if dir != "" {
		// dir is the repo path created by Sync (filepath.Join(dataDir, "repos", name))
		// or empty for initial clone; validated indirectly via validURL above.
		cmdArgs = append([]string{"git", "-C", dir}, args...)
	} else {
		cmdArgs = append([]string{"git"}, args...)
	}
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = cmdArgs
	// Drop any inherited GIT_DIR/GIT_WORK_TREE so the command targets dir (or the
	// clone destination), not an ambient repo leaked by a hook or bare worktree.
	cmd.Env = gitenv.Clean(cmd.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
