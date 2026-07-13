package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/store"
)

// TestResolveRegisteredPath_siblingWorktree is the audit regression (MÉDIA #6):
// index_worktree must accept a real git worktree of the same repo (worktrees are
// sibling directories, which a lexical path-prefix check wrongly rejected), while
// still refusing an unrelated path. Hermetic: all git runs with cmd.Dir in a
// tempdir, so the real repo is untouched.
func TestResolveRegisteredPath_siblingWorktree(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	wt := filepath.Join(root, "wt") // sibling worktree, NOT under repo/

	git := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	git(repo, "init", "-q")
	git(repo, "remote", "add", "origin", "https://example.com/acme/repo.git")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "-A")
	git(repo, "commit", "-q", "--no-verify", "-m", "c1")
	git(repo, "worktree", "add", "-q", wt) // linked worktree of the same repo

	ident := gitmeta.Resolve(t.Context(), repo).Identity
	if ident == "" {
		t.Skip("git identity not resolved in this environment")
	}

	fs := newFakeSearchStore()
	fs.addProject(&store.Project{Name: "p", Identity: ident, Path: repo, SourceType: "git", Model: "m"})

	// Sibling worktree of the SAME repo → allowed.
	_, target, _, err := resolveRegisteredPath(t.Context(), fs, indexWorktreeArgs{Project: "p", Path: wt})
	if err != nil {
		t.Fatalf("sibling worktree of the same repo should be allowed, got: %v", err)
	}
	if absWt, _ := filepath.Abs(wt); target != absWt {
		t.Errorf("target = %q, want the worktree path %q", target, absWt)
	}

	// An unrelated directory (different/absent identity) → still rejected.
	if _, _, _, err := resolveRegisteredPath(t.Context(), fs, indexWorktreeArgs{Project: "p", Path: t.TempDir()}); err == nil {
		t.Error("an unrelated path must still be rejected (guard preserved)")
	}
}
