package gitmeta

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitenv"
)

func TestNormalizeRemote(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/app.git":           "github.com/acme/app",
		"https://github.com/acme/app":               "github.com/acme/app",
		"https://user:pass@github.com/acme/app.git": "github.com/acme/app",
		"git@github.com:acme/app.git":               "github.com/acme/app",
		"ssh://git@github.com/acme/app.git":         "github.com/acme/app",
		"HTTPS://GitHub.com/Acme/App.git":           "github.com/acme/app",
		"https://github.com/acme/app/":              "github.com/acme/app",
	}
	for in, want := range cases {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

// gitInit makes a real repo in dir with hermetic config.
func gitInit(t *testing.T, dir string, args ...[]string) {
	t.Helper()
	base := [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@e.st"},
		{"config", "user.name", "t"},
	}
	for _, a := range append(base, args...) {
		cmd := exec.Command("git", append([]string{"-C", dir}, a...)...)
		cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

func TestResolveNonGit(t *testing.T) {
	if info := Resolve(context.Background(), t.TempDir()); info.IsGit {
		t.Errorf("non-git dir reported as git: %+v", info)
	}
}

func TestResolveWithRemote(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir, []string{"remote", "add", "origin", "git@github.com:acme/app.git"})
	info := Resolve(context.Background(), dir)
	if !info.IsGit {
		t.Fatal("expected git repo")
	}
	if info.Identity != "remote:github.com/acme/app" {
		t.Errorf("identity = %q, want remote:github.com/acme/app", info.Identity)
	}
	if info.Toplevel == "" {
		t.Error("toplevel empty")
	}
}

func TestResolveLocalNoRemote(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	info := Resolve(context.Background(), dir)
	if !info.IsGit || info.Identity == "" {
		t.Fatalf("info = %+v", info)
	}
	if got := info.Identity[:6]; got != "local:" {
		t.Errorf("identity = %q, want local: prefix", info.Identity)
	}
}

// TestWorktreesShareIdentity is the core F11 guarantee: two worktrees of the same
// repo resolve to the SAME identity but DIFFERENT toplevels.
func TestWorktreesShareIdentity(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo, []string{"remote", "add", "origin", "https://example.com/acme/app.git"})
	// A commit is required before `git worktree add`.
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo, []string{"add", "."}, []string{"commit", "-q", "-m", "init"})

	wt := filepath.Join(t.TempDir(), "wt")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", "-q", "-b", "feat", wt)
	cmd.Env = append(gitenv.Clean(os.Environ()), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v\n%s", err, out)
	}

	a := Resolve(context.Background(), repo)
	b := Resolve(context.Background(), wt)
	if a.Identity != b.Identity {
		t.Errorf("worktrees have different identities: %q vs %q", a.Identity, b.Identity)
	}
	if a.Toplevel == b.Toplevel {
		t.Errorf("worktrees share a toplevel (%q); should differ", a.Toplevel)
	}
}
