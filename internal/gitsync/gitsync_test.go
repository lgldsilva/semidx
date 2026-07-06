package gitsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitenv"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Isolate from any global/system git config (e.g. a core.hooksPath that would
	// run unrelated commit hooks) and from any inherited GIT_DIR/GIT_WORK_TREE (so
	// the command targets dir, not an ambient repo leaked by a hook/bare worktree).
	cmd.Env = append(gitenv.Clean(os.Environ()),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestSyncCloneThenPull(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src := t.TempDir()
	runGit(t, src, "init", "-q")
	runGit(t, src, "config", "user.email", "t@example.com")
	runGit(t, src, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-q", "-m", "first")

	data := t.TempDir()
	url := "file://" + src
	ctx := context.Background()

	path, err := Sync(ctx, data, "proj", url, "", true)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(path, "a.txt")); string(b) != "hello" {
		t.Errorf("cloned a.txt = %q, want hello", b)
	}

	// Advance the source, then a second Sync should fast-forward pull it.
	if err := os.WriteFile(filepath.Join(src, "b.txt"), []byte("world"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-q", "-m", "second")

	path2, err := Sync(ctx, data, "proj", url, "", true)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path2, "b.txt")); err != nil {
		t.Errorf("pull did not fetch b.txt: %v", err)
	}
}

func TestSyncRejectsUnsupportedURL(t *testing.T) {
	if _, err := Sync(context.Background(), t.TempDir(), "p", "ftp://evil/x", "", false); err == nil {
		t.Error("expected rejection of ftp:// url")
	}
}

func TestSyncRejectsFileURLByDefault(t *testing.T) {
	if _, err := Sync(context.Background(), t.TempDir(), "p", "file:///tmp/repo", "", false); err == nil {
		t.Error("expected rejection of file:// when allowFileURL is false")
	}
}
