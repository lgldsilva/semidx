package gitmeta

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestCanonicalOriginAlignsRedactedSSH(t *testing.T) {
	https := CanonicalOrigin("https://gitea.example/acme/sdk.git")
	ssh := CanonicalOrigin("git@gitea.example:acme/sdk.git")
	redacted := CanonicalOrigin("gitea.example:acme/sdk.git")
	if https != "gitea.example/acme/sdk" || ssh != https || redacted != https {
		t.Fatalf("CanonicalOrigin https=%q ssh=%q redacted=%q", https, ssh, redacted)
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
	if info.Origin != "git@github.com:acme/app.git" {
		t.Errorf("origin = %q, want git@github.com:acme/app.git", info.Origin)
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

// TestLocalReposDoNotCollide guards the identity of remote-less repositories:
// `git rev-parse --git-common-dir` answers ".git" at any repo root, so a raw
// value would give every local-only repo on the machine the same identity and
// let one project's index overwrite another's.
func TestLocalReposDoNotCollide(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	gitInit(t, a)
	gitInit(t, b)

	ia := Resolve(context.Background(), a)
	ib := Resolve(context.Background(), b)

	if ia.Identity == ib.Identity {
		t.Fatalf("distinct local repos share identity %q", ia.Identity)
	}
	for _, info := range []Info{ia, ib} {
		if strings.HasSuffix(info.Identity, ":.git") || strings.Contains(info.Identity, "..") {
			t.Errorf("identity %q is not an absolute common git dir", info.Identity)
		}
	}
}

// TestLocalIdentityStableFromSubdir pins the second half of the same bug: the
// raw --git-common-dir answer changes with the directory it is asked from
// ("../.git" one level down), which would split one repo into several projects.
func TestLocalIdentityStableFromSubdir(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	sub := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	root := Resolve(context.Background(), repo)
	deep := Resolve(context.Background(), sub)

	if root.Identity != deep.Identity {
		t.Errorf("identity depends on cwd: root %q, subdir %q", root.Identity, deep.Identity)
	}
	if root.Toplevel != deep.Toplevel {
		t.Errorf("toplevel = %q (root) vs %q (subdir)", root.Toplevel, deep.Toplevel)
	}
}

func TestResolveCommonDir(t *testing.T) {
	top := filepath.Join("/repos", "app")
	cases := []struct {
		name, dir, common, want string
	}{
		{"relative at repo root", "/repos/app", ".git", filepath.Join("/repos", "app", ".git")},
		{"relative from subdir", "/repos/app/pkg/deep", "../../.git", filepath.Join("/repos", "app", ".git")},
		{"already absolute", "/repos/app", "/repos/app/.git", filepath.Join("/repos", "app", ".git")},
		{"linked worktree common dir", "/repos/app-wt", "../app/.git", filepath.Join("/repos", "app", ".git")},
		{"empty falls back to toplevel", "/repos/app", "", top},
	}
	for _, c := range cases {
		if got := resolveCommonDir(c.dir, top, c.common); got != c.want {
			t.Errorf("%s: resolveCommonDir(%q, _, %q) = %q, want %q", c.name, c.dir, c.common, got, c.want)
		}
	}
}

// TestLocalIdentityFallsBackOnOldGit covers the compatibility path: git older
// than 2.31 has no --path-format, so Resolve must still turn the relative
// --git-common-dir answer into an absolute identity. A stub git on PATH rejects
// --path-format and delegates everything else to the real binary.
func TestLocalIdentityFallsBackOnOldGit(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	gitInit(t, repo)
	want := Resolve(context.Background(), repo).Identity

	stubDir := t.TempDir()
	stub := "#!/bin/sh\nfor a in \"$@\"; do\n  case \"$a\" in --path-format=*) echo \"error: unknown option\" >&2; exit 129;; esac\ndone\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(stubDir, "git"), []byte(stub), 0o700); err != nil { // #nosec G306 -- test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got := Resolve(context.Background(), repo)
	if got.Identity != want {
		t.Errorf("identity with old git = %q, want %q", got.Identity, want)
	}
	if strings.HasSuffix(got.Identity, ":.git") {
		t.Errorf("fallback kept the relative answer: %q", got.Identity)
	}
}
