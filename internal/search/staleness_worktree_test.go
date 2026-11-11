package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/localstore"
)

// TestAnnotateStaleness_perWorktreeVersion is the regression for staleness
// across worktrees: with "main" and "feat" both indexed (feat last, foo.go
// changed), a search from main where foo.go is unchanged must NOT be flagged
// stale, and a search from feat must compare against feat's own version.
// Before the fix, staleness used the most recently indexed hash of the path in
// the whole project (feat's), so main's fresh hit came back stale=true.
func TestAnnotateStaleness_perWorktreeVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mainDir, featDir := t.TempDir(), t.TempDir()
	mainContent := []byte("package foo\n// widget main version\n")
	featContent := []byte("package foo\n// widget feat version changed\n")
	pid, err := st.EnsureProjectIdentity(ctx, "remote:example.com/acme/app", "app", mainDir, "bge-m3", "git", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProjectIdentity(ctx, "remote:example.com/acme/app", "app", featDir, "bge-m3", "git", 0); err != nil {
		t.Fatal(err)
	}
	index := func(dir string, content []byte) {
		if err := os.WriteFile(filepath.Join(dir, "foo.go"), content, 0o600); err != nil {
			t.Fatal(err)
		}
		hash := indexing.ContentHash(content)
		fid, err := st.UpsertFile(ctx, pid, "foo.go", hash, len(content))
		if err != nil {
			t.Fatal(err)
		}
		chunks := []chunker.Chunk{{Content: string(content), StartLine: 1, EndLine: 2}}
		if err := st.InsertChunks(ctx, pid, fid, chunks, [][]float32{{1, 0, 0}}, 3); err != nil {
			t.Fatal(err)
		}
		if err := st.SetWorktreeFiles(ctx, pid, dir, map[string]string{"foo.go": hash}); err != nil {
			t.Fatal(err)
		}
	}
	index(mainDir, mainContent)
	index(featDir, featContent) // feat indexed last

	svc := NewService(st, &fakeEmbedder{vec: []float32{1, 0, 0}, dims: 3})
	search := func(wt string) bool {
		t.Helper()
		resp, err := svc.Search(ctx, Request{Identity: "remote:example.com/acme/app", Query: "widget", KeywordOnly: true, TopK: 5, Worktree: wt})
		if err != nil {
			t.Fatalf("Search(%s): %v", wt, err)
		}
		if len(resp.Results) != 1 {
			t.Fatalf("Search(%s) = %d results, want 1: %+v", wt, len(resp.Results), resp.Results)
		}
		if resp.Results[0].IndexedAt.IsZero() {
			t.Errorf("Search(%s): IndexedAt is zero", wt)
		}
		return resp.Results[0].Stale
	}

	if search(mainDir) {
		t.Error("main: unchanged foo.go flagged stale (compared against feat's hash)")
	}
	if search(featDir) {
		t.Error("feat: unchanged foo.go flagged stale")
	}
	// Edit main's checkout: now main's hit must be stale, feat stays fresh.
	if err := os.WriteFile(filepath.Join(mainDir, "foo.go"), featContent, 0o600); err != nil {
		t.Fatal(err)
	}
	if !search(mainDir) {
		t.Error("main: edited foo.go (now equal to feat's indexed version) must be stale")
	}
	if search(featDir) {
		t.Error("feat: unchanged foo.go flagged stale after main edit")
	}
}
