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
	for _, u := range []string{"https://x/y.git", "git@host:org/repo.git"} {
		if !validURL(u, false) {
			t.Errorf("validURL(%q, false) = false, want true", u)
		}
	}
	if !validURL("file:///srv/repo", true) {
		t.Error("file:// should be valid when allowFile is true")
	}
	if validURL("file:///srv/repo", false) {
		t.Error("file:// should be rejected when allowFile is false")
	}
	for _, u := range []string{"ftp://x", "http://x/y", "ssh://x", "", "/local/path", "git://x/y"} {
		if validURL(u, false) || validURL(u, true) {
			t.Errorf("validURL(%q) = true, want false", u)
		}
	}
}

// TestSyncCloneBranch covers the `branch != ""` clone argument path.
func TestSyncCloneBranch(t *testing.T) {
	needGit(t)
	src := initSource(t)
	runGit(t, src, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(src, "feat.txt"), []byte("on-feature"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "add", ".")
	runGit(t, src, "commit", "-q", "-m", "feature work")

	data := t.TempDir()
	path, err := Sync(context.Background(), data, "proj", "file://"+src, "feature", true)
	if err != nil {
		t.Fatalf("Sync(branch): %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "feat.txt")); err != nil {
		t.Errorf("branch clone did not check out the feature branch: %v", err)
	}
}

func TestSyncCloneFailure(t *testing.T) {
	needGit(t)
	data := t.TempDir()
	_, err := Sync(context.Background(), data, "proj", "file:///nonexistent/repo-does-not-exist", "", true)
	if err == nil || !strings.Contains(err.Error(), "git clone") {
		t.Fatalf("err = %v, want a git clone error", err)
	}
}

func TestSyncPullFailure(t *testing.T) {
	needGit(t)
	src := initSource(t)
	data := t.TempDir()
	url := "file://" + src

	repoPath, err := Sync(context.Background(), data, "proj", url, "", true)
	if err != nil {
		t.Fatalf("initial clone: %v", err)
	}

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

	_, err = Sync(context.Background(), data, "proj", url, "", true)
	if err == nil || !strings.Contains(err.Error(), "git pull") {
		t.Fatalf("err = %v, want a git pull error", err)
	}
}

func TestSyncCancelledContext(t *testing.T) {
	needGit(t)
	src := initSource(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Sync(ctx, t.TempDir(), "proj", "file://"+src, "", true); err == nil {
		t.Error("Sync under a cancelled context should error")
	}
}

func TestValidURLProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		suffix := rapid.String().Draw(rt, "suffix")

		for _, good := range []string{"https://", "git@"} {
			if !validURL(good+suffix, false) {
				rt.Fatalf("validURL(%q, false) = false, want true", good+suffix)
			}
		}
		if !validURL("file://"+suffix, true) {
			rt.Fatalf("validURL(file://%q, true) = false, want true", suffix)
		}
		if validURL("file://"+suffix, false) {
			rt.Fatalf("validURL(file://%q, false) = true, want false", suffix)
		}

		bad := rapid.SampledFrom([]string{"http://", "ftp://", "ssh://", "git://", "HTTPS://", "", "/", "./", "wss://"}).Draw(rt, "bad")
		full := bad + suffix
		want := strings.HasPrefix(full, "https://") || strings.HasPrefix(full, "git@")
		got := validURL(full, false)
		if got != want {
			rt.Fatalf("validURL(%q, false) = %v, want %v", full, got, want)
		}
	})
}

func TestSyncMkdirFailure(t *testing.T) {
	needGit(t)
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Sync(context.Background(), f, "proj", "file:///tmp/whatever", "", true)
	if err == nil {
		t.Fatal("expected MkdirAll to fail when dataDir is a regular file")
	}
}
