package gitsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// initSource creates a source repo with one commit and returns its path.
func initSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	runGit(t, src, "init", "-q")
	runGit(t, src, "config", "user.email", "t@example.com")
	runGit(t, src, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-q", "-m", "first")
	return src
}

func TestValidURL(t *testing.T) {
	ok := []string{"https://x/y.git", "git@host:org/repo.git", "file:///srv/repo"}
	for _, u := range ok {
		if !validURL(u) {
			t.Errorf("validURL(%q) = false, want true", u)
		}
	}
	bad := []string{"ftp://x", "http://x/y", "ssh://x", "", "/local/path", "git://x/y"}
	for _, u := range bad {
		if validURL(u) {
			t.Errorf("validURL(%q) = true, want false", u)
		}
	}
}

// TestSyncCloneBranch covers the `branch != ""` clone argument path.
func TestSyncCloneBranch(t *testing.T) {
	needGit(t)
	src := initSource(t)
	// Create and commit onto a named branch.
	runGit(t, src, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "feat.txt"), []byte("on-feature"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-q", "-m", "feature work")

	data := t.TempDir()
	path, err := Sync(context.Background(), data, "proj", "file://"+src, "feature")
	if err != nil {
		t.Fatalf("Sync(branch): %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "feat.txt")); err != nil {
		t.Errorf("branch clone did not check out the feature branch: %v", err)
	}
}

// TestSyncCloneFailure covers the clone error path (a file:// URL that passes
// validURL but points at no repository).
func TestSyncCloneFailure(t *testing.T) {
	needGit(t)
	data := t.TempDir()
	_, err := Sync(context.Background(), data, "proj", "file:///nonexistent/repo-does-not-exist", "")
	if err == nil || !strings.Contains(err.Error(), "git clone") {
		t.Fatalf("err = %v, want a git clone error", err)
	}
}

// TestSyncPullFailure covers the pull (--ff-only) error path: a divergent local
// checkout can't be fast-forwarded once the source advances differently.
func TestSyncPullFailure(t *testing.T) {
	needGit(t)
	src := initSource(t)
	data := t.TempDir()
	url := "file://" + src

	repoPath, err := Sync(context.Background(), data, "proj", url, "")
	if err != nil {
		t.Fatalf("initial clone: %v", err)
	}

	// Diverge: commit something different in both the clone and the source so a
	// fast-forward is impossible.
	runGit(t, repoPath, "config", "user.email", "c@example.com")
	runGit(t, repoPath, "config", "user.name", "cloner")
	if err := os.WriteFile(filepath.Join(repoPath, "local.txt"), []byte("local-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoPath, "add", ".")
	runGit(t, repoPath, "commit", "-q", "-m", "local divergent")

	if err := os.WriteFile(filepath.Join(src, "remote.txt"), []byte("remote-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-q", "-m", "remote divergent")

	_, err = Sync(context.Background(), data, "proj", url, "")
	if err == nil || !strings.Contains(err.Error(), "git pull") {
		t.Fatalf("err = %v, want a git pull error", err)
	}
}

// TestSyncCancelledContext: an already-cancelled context aborts the clone
// (exec.CommandContext kills the child), surfacing an error.
func TestSyncCancelledContext(t *testing.T) {
	needGit(t)
	src := initSource(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Sync(ctx, t.TempDir(), "proj", "file://"+src, ""); err == nil {
		t.Error("Sync under a cancelled context should error")
	}
}

// TestValidURLProperty: only the three accepted schemes pass, for any suffix,
// and no disallowed scheme is ever accepted. Kills mutations that broaden or
// swap an accepted prefix.
func TestValidURLProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		suffix := rapid.String().Draw(rt, "suffix")

		good := rapid.SampledFrom([]string{"https://", "git@", "file://"}).Draw(rt, "good")
		if !validURL(good + suffix) {
			rt.Fatalf("validURL(%q) = false, want true", good+suffix)
		}

		bad := rapid.SampledFrom([]string{"http://", "ftp://", "ssh://", "git://", "HTTPS://", "", "/", "./", "wss://"}).Draw(rt, "bad")
		got := validURL(bad + suffix)
		// The only way a "bad"+suffix can be valid is if the concatenation happens
		// to begin with an accepted prefix (e.g. bad="" and suffix="https://x").
		full := bad + suffix
		want := strings.HasPrefix(full, "https://") || strings.HasPrefix(full, "git@") || strings.HasPrefix(full, "file://")
		if got != want {
			rt.Fatalf("validURL(%q) = %v, want %v", full, got, want)
		}
	})
}

// TestSyncMkdirFailure covers the os.MkdirAll error branch: when dataDir is a
// regular file, creating dataDir/repos underneath it fails.
func TestSyncMkdirFailure(t *testing.T) {
	needGit(t)
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// dataDir is a file, so filepath.Dir(repoPath) = f/repos can't be created.
	_, err := Sync(context.Background(), f, "proj", "file:///tmp/whatever", "")
	if err == nil {
		t.Fatal("expected MkdirAll to fail when dataDir is a regular file")
	}
}
