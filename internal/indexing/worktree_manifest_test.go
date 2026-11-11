package indexing

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/localstore"
)

func newWorktreeTestStore(t *testing.T) (*localstore.SQLiteStore, int) {
	t.Helper()
	st, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)
	pid, err := st.EnsureProjectIdentity(context.Background(), "remote:example.com/acme/app", "app", "/wt/A", "m", "git", 3)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity: %v", err)
	}
	return st, pid
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
}

func worktreeIndexer(st *localstore.SQLiteStore, emb embed.Embedder) *Indexer {
	opts := IndexerOpts{Workers: 1, EmbedBatchSize: 64, MaxFileSize: 1 << 20, MaxChunksPerFile: 64}
	return NewIndexer(st, emb, 3, opts).SetWorktree("/wt/A")
}

func worktreePaths(t *testing.T, st *localstore.SQLiteStore, pid int) map[string]bool {
	t.Helper()
	res, err := st.SearchSimilarWorktree(context.Background(), pid, []float32{1, 0, 0}, 3, 100, "/wt/A")
	if err != nil {
		t.Fatalf("SearchSimilarWorktree: %v", err)
	}
	out := map[string]bool{}
	for _, r := range res {
		out[r.FilePath] = true
	}
	return out
}

// TestWorktreeEmbedFailureKeepsPreviousVersion is a regression test: a file
// whose re-embed failed returned no unit, so it was left out of the replaced
// worktree manifest and the prune deleted its previous good version — an
// embedding outage during reindex removed every changed file from search.
func TestWorktreeEmbedFailureKeepsPreviousVersion(t *testing.T) {
	ctx := context.Background()
	st, pid := newWorktreeTestStore(t)
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.go": "package a\n\nfunc A() {}\n", "b.go": "package a\n\nfunc B() {}\n"})

	if _, err := worktreeIndexer(st, &fakeEmbedder{}).IndexProject(ctx, pid, root, "m", 0); err != nil {
		t.Fatalf("first IndexProject: %v", err)
	}
	writeFiles(t, root, map[string]string{"a.go": "package a\n\nfunc A() { changed() }\n"})
	stats, err := worktreeIndexer(st, &alwaysFailEmbedder{}).IndexProject(ctx, pid, root, "m", 0)
	if err != nil {
		t.Fatalf("second IndexProject: %v", err)
	}
	if stats.Errors == 0 {
		t.Fatal("expected the failing embedder to report errors")
	}
	if got := worktreePaths(t, st, pid); !got["a.go"] || !got["b.go"] {
		t.Fatalf("worktree search paths = %v, want a.go (previous version) and b.go", got)
	}
}

// TestWorktreeMaxFilesKeepsUnvisitedFiles is a regression test: with
// --max-files truncating the scan, unvisited files dropped out of the
// manifest and were pruned.
func TestWorktreeMaxFilesKeepsUnvisitedFiles(t *testing.T) {
	ctx := context.Background()
	st, pid := newWorktreeTestStore(t)
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.go": "package a\n\nfunc A() {}\n", "b.go": "package a\n\nfunc B() {}\n"})

	if _, err := worktreeIndexer(st, &fakeEmbedder{}).IndexProject(ctx, pid, root, "m", 0); err != nil {
		t.Fatalf("first IndexProject: %v", err)
	}
	if _, err := worktreeIndexer(st, &fakeEmbedder{}).IndexProject(ctx, pid, root, "m", 1); err != nil {
		t.Fatalf("truncated IndexProject: %v", err)
	}
	if got := worktreePaths(t, st, pid); !got["a.go"] || !got["b.go"] {
		t.Fatalf("worktree search paths = %v, want both files after a --max-files run", got)
	}
}

// TestWorktreeDeletedFileStillPruned guards the carry-over: a file removed from
// disk must still leave the manifest and be pruned.
func TestWorktreeDeletedFileStillPruned(t *testing.T) {
	ctx := context.Background()
	st, pid := newWorktreeTestStore(t)
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"a.go": "package a\n\nfunc A() {}\n", "b.go": "package a\n\nfunc B() {}\n"})
	if _, err := worktreeIndexer(st, &fakeEmbedder{}).IndexProject(ctx, pid, root, "m", 0); err != nil {
		t.Fatalf("first IndexProject: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := worktreeIndexer(st, &fakeEmbedder{}).IndexProject(ctx, pid, root, "m", 0); err != nil {
		t.Fatalf("second IndexProject: %v", err)
	}
	if got := worktreePaths(t, st, pid); got["b.go"] || !got["a.go"] {
		t.Fatalf("worktree search paths = %v, want only a.go", got)
	}
	hashes, err := st.ListFileHashes(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hashes["b.go"]; ok {
		t.Fatal("deleted b.go was not pruned")
	}
}
