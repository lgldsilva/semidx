package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitenv"
)

func TestRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	runCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Init a real git repo.
	runCmd("init", "--initial-branch=main")
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0o644)

	// Configure user (needed for commit).
	runCmd("config", "user.name", "test")
	runCmd("config", "user.email", "test@test")
	runCmd("config", "core.hooksPath", "")

	runCmd("add", ".")
	runCmd("commit", "-m", "chore: initial")

	t.Run("rev-parse", func(t *testing.T) {
		out, err := Run(context.Background(), dir, "rev-parse", "--short", "HEAD")
		if err != nil {
			t.Fatalf("Run rev-parse: %v", err)
		}
		if len(out) != 7 {
			t.Errorf("rev-parse output = %q, want 7-char SHA", out)
		}
	})

	t.Run("unsafe path with ..", func(t *testing.T) {
		if _, err := Run(context.Background(), "/tmp/../etc"); err == nil {
			t.Error("expected error for path with ..")
		}
	})

	t.Run("unsafe path with -", func(t *testing.T) {
		if _, err := Run(context.Background(), "-danger"); err == nil {
			t.Error("expected error for path starting with -")
		}
	})

	t.Run("unsafe path with ~", func(t *testing.T) {
		if _, err := Run(context.Background(), "~/evil"); err == nil {
			t.Error("expected error for path starting with ~")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Run(ctx, dir, "rev-parse", "HEAD"); err == nil {
			t.Error("expected error for cancelled context")
		}
	})

	t.Run("non-git directory", func(t *testing.T) {
		if _, err := Run(context.Background(), t.TempDir(), "rev-parse", "HEAD"); err == nil {
			t.Error("expected error in non-git directory")
		}
	})
}
