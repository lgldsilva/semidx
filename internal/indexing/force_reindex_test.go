package indexing

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/localstore"
)

// TestForceReindexEmbedFailureKeepsSharedVersion is a regression test: a
// --force pass re-used the existing content-addressed (path, hash) row, deleted
// its chunks before embedding, and on embed failure rolled back by deleting
// that row — destroying the version every worktree on that hash searches.
func TestForceReindexEmbedFailureKeepsSharedVersion(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)
	pid, err := st.EnsureProjectIdentity(ctx, "remote:example.com/acme/app", "app", "/wt/A", "m", "git", 3)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity: %v", err)
	}
	content := []byte("package a\n\nfunc Shared() int { return 42 }\n")
	opts := IndexerOpts{Workers: 1, EmbedBatchSize: 64, MaxFileSize: 1 << 20, MaxChunksPerFile: 64}

	good := NewIndexer(st, &fakeEmbedder{}, 3, opts)
	if _, err := good.IndexContent(ctx, pid, "a.go", "m", content); err != nil {
		t.Fatalf("seed IndexContent: %v", err)
	}

	// A different model name bypasses the embedding cache, so the forced pass
	// really calls the (failing) embedder.
	forced := NewIndexer(st, &alwaysFailEmbedder{}, 3, opts).SetForce(true)
	if _, err := forced.IndexContent(ctx, pid, "a.go", "m2", content); err != nil {
		t.Fatalf("forced IndexContent: %v", err)
	}

	upToDate, err := st.FileUpToDate(ctx, pid, "a.go", ContentHash(content), 3)
	if err != nil || !upToDate {
		t.Fatalf("FileUpToDate after failed force = %v, %v; want true (existing version preserved)", upToDate, err)
	}
	res, err := st.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 10)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("search lost the shared version after a failed --force reindex")
	}
}
