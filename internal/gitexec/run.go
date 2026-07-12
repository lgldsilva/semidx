// Package gitexec provides a safe, hermetic git runner shared by gitmeta
// (identity resolution) and repotools (worktree/branch/status tools).
// The security checks and env cleansing must not be duplicated — use this
// package whenever semidx needs to execute a git subcommand.
package gitexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitenv"
)

// Run executes a git subcommand in dir with hermetic config (no global/system
// config, so tests and odd environments behave predictably) and returns trimmed
// stdout. It rejects unsafe directories (containing "..", starting with "-" or
// "~") to prevent path-traversal / injection.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	if strings.Contains(dir, "..") || strings.HasPrefix(dir, "-") || strings.HasPrefix(dir, "~") {
		return "", fmt.Errorf("unsafe git directory: %q", dir)
	}
	fullArgs := append([]string{"git", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = fullArgs
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
