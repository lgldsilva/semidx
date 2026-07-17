package localstore

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// TestLookupEmbeddingCacheEmptyAndMiss covers the empty-input fast path and
// a cache miss for unknown hashes.
// coverage-patch: 2026-07-17
func TestLookupEmbeddingCacheEmptyAndMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	_ = s.EnsureEmbeddingCacheTable(ctx, 3)

	empty, err := s.LookupEmbeddingCache(ctx, nil, "bge-m3", 3)
	if err != nil {
		t.Fatalf("LookupEmbeddingCache(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("LookupEmbeddingCache(nil) = %+v, want empty map", empty)
	}

	empty2, err := s.LookupEmbeddingCache(ctx, []string{}, "bge-m3", 3)
	if err != nil || len(empty2) != 0 {
		t.Fatalf("LookupEmbeddingCache([]) = %+v err=%v", empty2, err)
	}

	miss, err := s.LookupEmbeddingCache(ctx, []string{"no-such-hash"}, "bge-m3", 3)
	if err != nil {
		t.Fatalf("LookupEmbeddingCache(miss): %v", err)
	}
	if len(miss) != 0 {
		t.Fatalf("LookupEmbeddingCache(miss) = %+v, want empty", miss)
	}
}

// TestInsertEmbeddingCacheLengthMismatchAndEmpty covers length-guard and
// empty-batch early return.
// coverage-patch: 2026-07-17
func TestInsertEmbeddingCacheLengthMismatchAndEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	_ = s.EnsureEmbeddingCacheTable(ctx, 3)

	if err := s.InsertEmbeddingCache(ctx, []string{"a"}, "m", [][]float32{{1, 0}, {0, 1}}, 3); err == nil {
		t.Fatal("InsertEmbeddingCache length mismatch: want error")
	}
	if err := s.InsertEmbeddingCache(ctx, []string{"a", "b"}, "m", [][]float32{{1, 0, 0}}, 3); err == nil {
		t.Fatal("InsertEmbeddingCache length mismatch (hashes > embs): want error")
	}
	if err := s.InsertEmbeddingCache(ctx, nil, "m", nil, 3); err != nil {
		t.Fatalf("InsertEmbeddingCache(empty): %v", err)
	}
	if err := s.InsertEmbeddingCache(ctx, []string{}, "m", [][]float32{}, 3); err != nil {
		t.Fatalf("InsertEmbeddingCache([]): %v", err)
	}
}

// TestGetProjectByIDNotFound covers ErrNotFound for a missing project id.
// coverage-patch: 2026-07-17
func TestGetProjectByIDNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.GetProjectByID(ctx, 99999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProjectByID(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetProject(ctx, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject(ghost) err = %v, want ErrNotFound", err)
	}
}

// TestFileUpToDateMiss covers unknown path / wrong hash / wrong dims misses.
// coverage-patch: 2026-07-17
func TestFileUpToDateMiss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "fud-miss", "/tmp/fud", "bge-m3", 0)
	if up, err := s.FileUpToDate(ctx, pid, "missing.go", "h", 3); err != nil || up {
		t.Fatalf("FileUpToDate(missing) = %v err=%v, want false", up, err)
	}

	fid, _ := s.UpsertFile(ctx, pid, "a.go", "hash-a", 10)
	_ = s.InsertChunks(ctx, pid, fid, []chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0, 0}}, 3)

	if up, err := s.FileUpToDate(ctx, pid, "a.go", "wrong-hash", 3); err != nil || up {
		t.Fatalf("FileUpToDate(wrong hash) = %v err=%v, want false", up, err)
	}
	if up, err := s.FileUpToDate(ctx, 99999, "a.go", "hash-a", 3); err != nil || up {
		t.Fatalf("FileUpToDate(bad project) = %v err=%v, want false", up, err)
	}
}

// TestDeleteFileByPathAndDeleteProject covers happy path, missing file no-op,
// and DeleteProject missing/happy.
// coverage-patch: 2026-07-17
func TestDeleteFileByPathAndDeleteProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "del-edge", "/tmp/de", "bge-m3", 0)
	fid, _ := s.UpsertFile(ctx, pid, "keep.go", "h1", 10)
	_ = s.InsertChunks(ctx, pid, fid, []chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0}}, 2)
	_, _ = s.UpsertFile(ctx, pid, "gone.go", "h2", 20)

	// Missing path is a no-op (no error).
	if err := s.DeleteFileByPath(ctx, pid, "never-existed.go"); err != nil {
		t.Fatalf("DeleteFileByPath(missing): %v", err)
	}

	if err := s.DeleteFileByPath(ctx, pid, "gone.go"); err != nil {
		t.Fatalf("DeleteFileByPath(gone.go): %v", err)
	}
	hashes, err := s.ListFileHashes(ctx, pid)
	if err != nil || len(hashes) != 1 || hashes["keep.go"] != "h1" {
		t.Fatalf("after delete hashes = %v err=%v", hashes, err)
	}

	if err := s.DeleteProject(ctx, "no-such-project"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteProject(missing) err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteProject(ctx, "del-edge"); err != nil {
		t.Fatalf("DeleteProject(happy): %v", err)
	}
	if _, err := s.GetProject(ctx, "del-edge"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject after delete err = %v, want ErrNotFound", err)
	}
}

// TestCosineSimilarityEdgeCases covers length mismatch and zero-vector paths.
// coverage-patch: 2026-07-17
func TestCosineSimilarityEdgeCases(t *testing.T) {
	t.Parallel()

	if got := cosineSimilarity([]float32{1, 0}, []float32{1}); got != 0 {
		t.Fatalf("different lengths: got %v, want 0", got)
	}
	if got := cosineSimilarity(nil, nil); got != 0 {
		t.Fatalf("empty: got %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{}, []float32{}); got != 0 {
		t.Fatalf("zero-len: got %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{0, 0, 0}, []float32{1, 2, 3}); got != 0 {
		t.Fatalf("zero vector a: got %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{1, 2, 3}, []float32{0, 0, 0}); got != 0 {
		t.Fatalf("zero vector b: got %v, want 0", got)
	}
	// Orthogonal unit vectors → 0; parallel → 1.
	if got := cosineSimilarity([]float32{1, 0}, []float32{0, 1}); got != 0 {
		t.Fatalf("orthogonal: got %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{1, 0}, []float32{1, 0}); got < 0.99 {
		t.Fatalf("parallel: got %v, want ~1", got)
	}
}

// TestInsertChunksEmptyAndMismatch covers empty batch and length mismatch.
// coverage-patch: 2026-07-17
func TestInsertChunksEmptyAndMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "ins-edge", "/tmp/ie", "bge-m3", 0)
	fid, _ := s.UpsertFile(ctx, pid, "a.go", "h", 10)

	if err := s.InsertChunks(ctx, pid, fid, nil, nil, 3); err != nil {
		t.Fatalf("InsertChunks(nil): %v", err)
	}
	if err := s.InsertChunks(ctx, pid, fid, []chunker.Chunk{}, [][]float32{}, 3); err != nil {
		t.Fatalf("InsertChunks(empty): %v", err)
	}
	if err := s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}},
		[][]float32{{1, 0}, {0, 1}},
		2,
	); err == nil {
		t.Fatal("InsertChunks length mismatch: want error")
	}
	if err := s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "a"}, {Content: "b"}},
		[][]float32{{1, 0}},
		2,
	); err == nil {
		t.Fatal("InsertChunks (more chunks than embs): want error")
	}
}

// TestPruneUnreferencedFilesWithRealFiles then prunes orphans.
// coverage-patch: 2026-07-17
func TestPruneUnreferencedFilesWithRealFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "prune-real", "/tmp/pr", "bge-m3", 0)
	fKeep, _ := s.UpsertFile(ctx, pid, "kept.go", "hk", 10)
	fDrop, _ := s.UpsertFile(ctx, pid, "orphan.go", "ho", 20)
	_ = s.InsertChunks(ctx, pid, fKeep, []chunker.Chunk{{Content: "keep", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0}}, 2)
	_ = s.InsertChunks(ctx, pid, fDrop, []chunker.Chunk{{Content: "drop", StartLine: 1, EndLine: 1}}, [][]float32{{0, 1}}, 2)
	_ = s.InsertFileDependencies(ctx, pid, "orphan.go", []string{"kept.go"})

	if err := s.SetWorktreeFiles(ctx, pid, "main", map[string]string{"kept.go": "hk"}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}

	n, err := s.PruneUnreferencedFiles(ctx, pid)
	if err != nil {
		t.Fatalf("PruneUnreferencedFiles: %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneUnreferencedFiles removed %d, want 1", n)
	}
	hashes, _ := s.ListFileHashes(ctx, pid)
	if len(hashes) != 1 || hashes["kept.go"] != "hk" {
		t.Fatalf("hashes after prune = %v", hashes)
	}
	// No worktree refs → prune all remaining.
	_ = s.SetWorktreeFiles(ctx, pid, "main", map[string]string{})
	n2, err := s.PruneUnreferencedFiles(ctx, pid)
	if err != nil || n2 != 1 {
		t.Fatalf("second prune = %d err=%v, want 1", n2, err)
	}
}

// TestDropAllLeavesEmptyStore verifies DropAll empties projects and allows re-use.
// coverage-patch: 2026-07-17
func TestDropAllLeavesEmptyStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "drop-me", "/tmp/dm", "bge-m3", 0)
	fid, _ := s.UpsertFile(ctx, pid, "a.go", "h", 5)
	_ = s.InsertChunks(ctx, pid, fid, []chunker.Chunk{{Content: "z", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0}}, 2)
	_ = s.EnsureEmbeddingCacheTable(ctx, 2)
	_ = s.InsertEmbeddingCache(ctx, []string{"c1"}, "bge-m3", [][]float32{{1, 0}}, 2)

	if err := s.DropAll(ctx); err != nil {
		t.Fatalf("DropAll: %v", err)
	}
	list, err := s.ListProjects(ctx, 0, 0)
	if err != nil || len(list) != 0 {
		t.Fatalf("after DropAll projects = %d err=%v, want 0", len(list), err)
	}
	// Store is usable again.
	id, err := s.UpsertProject(ctx, "after-drop", "/tmp/ad", "bge-m3", 0)
	if err != nil || id < 1 {
		t.Fatalf("UpsertProject after DropAll id=%d err=%v", id, err)
	}
}

// TestCreateProjectUniqueViolation maps duplicate name to ErrProjectExists.
// coverage-patch: 2026-07-17
func TestCreateProjectUniqueViolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	p, err := s.CreateProject(ctx, "uniq-proj", "bge-m3", "git", "https://example.com/r.git", "main", 0)
	if err != nil || p == nil || p.Name != "uniq-proj" {
		t.Fatalf("CreateProject = %+v err=%v", p, err)
	}
	if _, err := s.CreateProject(ctx, "uniq-proj", "other", "push", "", "", 0); !errors.Is(err, store.ErrProjectExists) {
		t.Fatalf("duplicate CreateProject err = %v, want ErrProjectExists", err)
	}
}
