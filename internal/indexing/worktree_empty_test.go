package indexing

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/localstore"
)

// TestWatcherEmptyWorktreeFileKeepsOtherVersion proves that a watcher update
// for worktree A cannot delete the divergent version currently checked out by
// worktree B. The real SQLite store exercises the scoped manifest removal and
// subsequent orphan pruning rather than a test double.
func TestWatcherEmptyWorktreeFileKeepsOtherVersion(t *testing.T) {
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
	seed := func(worktree, hash, content string) {
		t.Helper()
		fileID, err := st.UpsertFile(ctx, pid, "shared.go", hash, len(content))
		if err != nil {
			t.Fatalf("UpsertFile(%s): %v", worktree, err)
		}
		chunks := []chunker.Chunk{{Content: content, StartLine: 1, EndLine: 1}}
		if err := st.InsertChunks(ctx, pid, fileID, chunks, [][]float32{{1, 0, 0}}, 3); err != nil {
			t.Fatalf("InsertChunks(%s): %v", worktree, err)
		}
		if err := st.SetWorktreeFiles(ctx, pid, worktree, map[string]string{"shared.go": hash}); err != nil {
			t.Fatalf("SetWorktreeFiles(%s): %v", worktree, err)
		}
	}
	seed("/wt/A", "hash-A", "A-version")
	seed("/wt/B", "hash-B", "B-version")

	root := t.TempDir()
	path := filepath.Join(root, "shared.go")
	if err := os.WriteFile(path, []byte(" \n\t"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	idx := NewIndexer(st, nil, 3, IndexerOpts{Workers: 1})
	idx.SetWorktree("/wt/A")
	w := NewWatcher(pid, root, "m", idx)
	w.handleCreate(ctx, path)

	resA, err := st.SearchSimilarWorktree(ctx, pid, []float32{1, 0, 0}, 3, 10, "/wt/A")
	if err != nil {
		t.Fatalf("SearchSimilarWorktree(A): %v", err)
	}
	if len(resA) != 0 {
		t.Fatalf("worktree A still sees removed content: %+v", resA)
	}
	resB, err := st.SearchSimilarWorktree(ctx, pid, []float32{1, 0, 0}, 3, 10, "/wt/B")
	if err != nil {
		t.Fatalf("SearchSimilarWorktree(B): %v", err)
	}
	if len(resB) != 1 || resB[0].Content != "B-version" {
		t.Fatalf("worktree B content = %+v, want B-version", resB)
	}

	hashes, err := st.ListFileHashes(ctx, pid)
	if err != nil {
		t.Fatalf("ListFileHashes: %v", err)
	}
	if got := hashes["shared.go"]; got != "hash-B" {
		t.Fatalf("remaining shared.go hash = %q, want hash-B", got)
	}
}

func TestWatcherRemoveWorktreeFileKeepsOtherVersion(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	pid, err := st.EnsureProjectIdentity(ctx, "remote:example.com/acme/remove", "app", "/wt/A", "m", "git", 3)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity: %v", err)
	}
	for _, version := range []struct {
		worktree string
		hash     string
		content  string
	}{
		{"/wt/A", "hash-A", "A-version"},
		{"/wt/B", "hash-B", "B-version"},
	} {
		fileID, err := st.UpsertFile(ctx, pid, "removed.go", version.hash, len(version.content))
		if err != nil {
			t.Fatalf("UpsertFile: %v", err)
		}
		if err := st.InsertChunks(ctx, pid, fileID,
			[]chunker.Chunk{{Content: version.content, StartLine: 1, EndLine: 1}},
			[][]float32{{1, 0, 0}}, 3); err != nil {
			t.Fatalf("InsertChunks: %v", err)
		}
		if err := st.SetWorktreeFiles(ctx, pid, version.worktree, map[string]string{"removed.go": version.hash}); err != nil {
			t.Fatalf("SetWorktreeFiles: %v", err)
		}
	}

	root := t.TempDir()
	idx := NewIndexer(st, nil, 3, IndexerOpts{})
	idx.SetWorktree("/wt/A")
	w := NewWatcher(pid, root, "m", idx)
	w.handleRemove(ctx, filepath.Join(root, "removed.go"))

	resB, err := st.SearchSimilarWorktree(ctx, pid, []float32{1, 0, 0}, 3, 10, "/wt/B")
	if err != nil {
		t.Fatalf("SearchSimilarWorktree(B): %v", err)
	}
	if len(resB) != 1 || resB[0].Content != "B-version" {
		t.Fatalf("worktree B content after remove = %+v, want B-version", resB)
	}
}
