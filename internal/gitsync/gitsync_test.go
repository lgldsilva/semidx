package gitsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Regression: a project name such as "../x" escaped DataDir/repos.
func TestSyncRejectsNonLocalName(t *testing.T) {
	data := t.TempDir()
	for _, name := range []string{"../escape", "/abs", "", "a/../../b"} {
		_, err := Sync(context.Background(), data, name, "https://example.invalid/r.git", "", false)
		if err == nil || !strings.Contains(err.Error(), "invalid repo name") {
			t.Errorf("Sync(%q) err = %v, want invalid repo name", name, err)
		}
	}
}

func initRepo(t *testing.T, file, content string) string {
	t.Helper()
	src := t.TempDir()
	runGit(t, src, "init", "-q")
	runGit(t, src, "config", "user.email", "t@example.com")
	runGit(t, src, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(src, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-q", "-m", "init")
	return src
}

// Regression: an existing checkout was pulled without checking its origin, so
// a project whose URL differs (another tenant, or an edited git_url) reused a
// foreign clone. The sync must re-clone from the project's own URL.
func TestSyncReclonesWhenOriginDiffers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srcA := initRepo(t, "a.txt", "tenant-a")
	srcB := initRepo(t, "b.txt", "tenant-b")
	data := t.TempDir()
	ctx := context.Background()

	if _, err := Sync(ctx, data, "1/7", "file://"+srcA, "", true); err != nil {
		t.Fatalf("clone A: %v", err)
	}
	path, err := Sync(ctx, data, "1/7", "file://"+srcB, "", true)
	if err != nil {
		t.Fatalf("sync B: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "a.txt")); err == nil {
		t.Fatal("checkout still holds the foreign repo's a.txt")
	}
	if b, _ := os.ReadFile(filepath.Join(path, "b.txt")); string(b) != "tenant-b" {
		t.Fatalf("b.txt = %q, want tenant-b", b)
	}
}
