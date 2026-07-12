package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Init a real git repo.
	init := exec.Command("git", "init", "--initial-branch=main")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0o644)

	// Configure user (needed for commit).
	for _, cfg := range []struct{ k, v string }{
		{"user.name", "test"},
		{"user.email", "test@test"},
	} {
		c := exec.Command("git", "config", cfg.k, cfg.v)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", cfg.k, err, out)
		}
	}
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commit := exec.Command("git", "commit", "-m", "chore: initial")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

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
