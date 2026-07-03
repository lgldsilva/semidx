package localstore

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// TestWorktreeDivergentContent is the F11 core guarantee: one project (repo
// identity) can hold divergent versions of the same path across worktrees;
// each worktree's search returns ITS version, identical content is shared, and
// pruning drops versions no worktree references.
func TestWorktreeDivergentContent(t *testing.T) {
	ctx := context.Background()
	s, err := New(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Two worktrees of the same repo map to ONE project (by identity).
	pidA, err := s.EnsureProjectIdentity(ctx, "remote:example.com/acme/app", "app", "/wt/A", "bge-m3", "git")
	if err != nil {
		t.Fatal(err)
	}
	pidB, err := s.EnsureProjectIdentity(ctx, "remote:example.com/acme/app", "app", "/wt/B", "bge-m3", "git")
	if err != nil {
		t.Fatal(err)
	}
	if pidA != pidB {
		t.Fatalf("worktrees mapped to different projects: %d vs %d", pidA, pidB)
	}
	pid := pidA

	// index installs a version of auth.go with distinct content+hash+embedding for
	// a worktree, then records the worktree's manifest.
	index := func(worktree, hash, content string, vec []float32) {
		fid, err := s.UpsertFile(ctx, pid, "auth.go", hash, len(content))
		if err != nil {
			t.Fatal(err)
		}
		chunks := []chunker.Chunk{{Content: content, StartLine: 1, EndLine: 1}}
		if err := s.InsertChunks(ctx, pid, fid, chunks, [][]float32{vec}, 3); err != nil {
			t.Fatal(err)
		}
		if err := s.SetWorktreeFiles(ctx, pid, worktree, map[string]string{"auth.go": hash}); err != nil {
			t.Fatal(err)
		}
	}
	index("/wt/A", "hashA", "A-version argon2id", []float32{1, 0, 0})
	index("/wt/B", "hashB", "B-version bcrypt", []float32{0, 1, 0})

	q := []float32{1, 1, 1}
	// Worktree A sees ONLY its version.
	resA, err := s.SearchSimilarWorktree(ctx, pid, q, 3, 10, "/wt/A")
	if err != nil {
		t.Fatal(err)
	}
	if !onlyContains(resA, "A-version") {
		t.Errorf("worktree A search leaked another version: %+v", resA)
	}
	// Worktree B sees ONLY its version.
	resB, _ := s.SearchSimilarWorktree(ctx, pid, q, 3, 10, "/wt/B")
	if !onlyContains(resB, "B-version") {
		t.Errorf("worktree B search leaked another version: %+v", resB)
	}
	// Unfiltered search sees BOTH divergent versions coexisting.
	all, _ := s.SearchSimilar(ctx, pid, q, 3, 10)
	if len(all) != 2 {
		t.Errorf("unfiltered search = %d results, want 2 (both versions)", len(all))
	}

	// Drop A's checkout of auth.go, then prune: A's version (hashA) is now
	// unreferenced and removed; B's remains.
	if err := s.SetWorktreeFiles(ctx, pid, "/wt/A", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneUnreferencedFiles(ctx, pid)
	if err != nil || n != 1 {
		t.Fatalf("prune removed %d files, err %v; want 1", n, err)
	}
	if remaining, _ := s.SearchSimilar(ctx, pid, q, 3, 10); len(remaining) != 1 || !strings.Contains(remaining[0].Content, "B-version") {
		t.Errorf("after prune, remaining = %+v; want only B-version", remaining)
	}
}

// TestWorktreeSharedContentDedups verifies identical content across worktrees is
// stored once (the re-index optimization): the second worktree's identical file
// reuses the same file row, and FileUpToDate short-circuits it.
func TestWorktreeSharedContentDedups(t *testing.T) {
	ctx := context.Background()
	s, err := New(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, _ := s.EnsureProjectIdentity(ctx, "remote:x", "app", "/wt/A", "m", "git")

	fid1, _ := s.UpsertFile(ctx, pid, "shared.go", "hSame", 10)
	_ = s.InsertChunks(ctx, pid, fid1, []chunker.Chunk{{Content: "same", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0, 0}}, 3)

	// Same (path, hash) from another worktree returns the SAME file row and is
	// reported up-to-date (no re-embedding needed).
	fid2, _ := s.UpsertFile(ctx, pid, "shared.go", "hSame", 10)
	if fid1 != fid2 {
		t.Errorf("identical content got a new file row: %d vs %d", fid1, fid2)
	}
	up, err := s.FileUpToDate(ctx, pid, "shared.go", "hSame", 3)
	if err != nil || !up {
		t.Errorf("FileUpToDate for shared content = %v, err %v; want true", up, err)
	}
}

func onlyContains(rs []store.SearchResult, want string) bool {
	if len(rs) == 0 {
		return false
	}
	for _, r := range rs {
		if !strings.Contains(r.Content, want) {
			return false
		}
	}
	return true
}
