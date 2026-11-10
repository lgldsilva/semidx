package indexing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/localstore"
)

func docsTestStore(t *testing.T) (*localstore.SQLiteStore, int) {
	t.Helper()
	st, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)
	pid, err := st.EnsureProjectIdentity(context.Background(), "path:/docs", "docs", "/docs", "m", "docs", 3)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity: %v", err)
	}
	return st, pid
}

func docsIndex(t *testing.T, st *localstore.SQLiteStore, pid int, root string) {
	t.Helper()
	idx := NewIndexer(st, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, MaxFileSize: 1 << 20, MaxChunksPerFile: 64}).
		SetPruneMissing(true)
	if _, err := idx.IndexProject(context.Background(), pid, root, "m", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
}

func storedHashes(t *testing.T, st *localstore.SQLiteStore, pid int) map[string]string {
	t.Helper()
	h, err := st.ListFileHashes(context.Background(), pid)
	if err != nil {
		t.Fatalf("ListFileHashes: %v", err)
	}
	return h
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDocsSubfolderGitIncremental is a regression test for --docs on a
// subfolder of a git repo: the git-diff incremental path joined repo-root
// paths onto the subfolder, ignored uncommitted changes, dropped quoted
// non-ASCII names, kept the old name of a renamed file and never removed
// deleted files.
func TestDocsSubfolderGitIncremental(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.email", "t@example.com")
	runGit(t, repo, "config", "user.name", "tester")
	sub := filepath.Join(repo, "docs")
	write(t, filepath.Join(repo, "root.md"), "root file\n")
	write(t, filepath.Join(sub, "a.md"), "alpha v1\n")
	write(t, filepath.Join(sub, "b.md"), "bravo v1\n")
	write(t, filepath.Join(sub, "c.md"), "charlie v1\n")
	write(t, filepath.Join(sub, "d.md"), "delta v1\n")
	write(t, filepath.Join(sub, "ção.md"), "acento v1\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "init")

	st, pid := docsTestStore(t)
	docsIndex(t, st, pid, sub)

	write(t, filepath.Join(sub, "a.md"), "alpha v2 committed\n")
	write(t, filepath.Join(sub, "ção.md"), "acento v2\n")
	runGit(t, repo, "rm", "-q", "docs/c.md")
	runGit(t, repo, "mv", "docs/d.md", "docs/e.md")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "change")
	write(t, filepath.Join(sub, "b.md"), "bravo v2 uncommitted\n")

	docsIndex(t, st, pid, sub)
	got := storedHashes(t, st, pid)
	want := map[string]string{
		"a.md":   "alpha v2 committed\n",
		"b.md":   "bravo v2 uncommitted\n",
		"e.md":   "delta v1\n",
		"ção.md": "acento v2\n",
	}
	for path, content := range want {
		if got[path] != ContentHash([]byte(content)) {
			t.Errorf("%s: stored hash %q does not match current content %q", path, got[path], content)
		}
	}
	for _, gone := range []string{"c.md", "d.md"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%s still indexed after it was removed", gone)
		}
	}
	if len(got) != len(want) {
		t.Errorf("stored paths = %v, want exactly %v", got, want)
	}
	if n, err := st.CountProjectFiles(context.Background(), pid); err != nil || n != len(want) {
		t.Errorf("CountProjectFiles = %d, %v; want %d (superseded versions pruned)", n, err, len(want))
	}
}

// TestDocsFullScanPrunesMissing is a regression test: a plain (non-git) docs
// folder never removed files deleted from disk.
func TestDocsFullScanPrunesMissing(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "keep.md"), "keep\n")
	write(t, filepath.Join(root, "gone.md"), "gone\n")
	st, pid := docsTestStore(t)
	docsIndex(t, st, pid, root)
	if err := os.Remove(filepath.Join(root, "gone.md")); err != nil {
		t.Fatal(err)
	}
	docsIndex(t, st, pid, root)
	got := storedHashes(t, st, pid)
	if _, ok := got["gone.md"]; ok {
		t.Fatalf("gone.md still indexed: %v", got)
	}
	if _, ok := got["keep.md"]; !ok {
		t.Fatalf("keep.md missing: %v", got)
	}
}
