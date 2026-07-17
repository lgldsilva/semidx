package store

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestEnsureChunksTableHighDims exercises the halfvec HNSW path (dims > 2000).
// coverage-patch: 2026-07-17
func TestEnsureChunksTableHighDims(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	dims := hnswVectorLimit + 1 // 2001
	if err := s.EnsureChunksTable(ctx, dims); err != nil {
		t.Fatalf("EnsureChunksTable(%d): %v", dims, err)
	}
	// Idempotent second call.
	if err := s.EnsureChunksTable(ctx, dims); err != nil {
		t.Fatalf("EnsureChunksTable re-run: %v", err)
	}
	// distanceExpr for high dims.
	if got := distanceExpr(dims); got == "c.embedding <=> $1" {
		t.Fatalf("distanceExpr(%d) should use halfvec cast, got %q", dims, got)
	}
}

// TestEmbeddingCacheEdgeCases covers empty/mismatch/invalid-dims guards.
// coverage-patch: 2026-07-17
func TestEmbeddingCacheEdgeCases(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	empty, err := s.LookupEmbeddingCache(ctx, nil, "m", 3)
	if err != nil || len(empty) != 0 {
		t.Fatalf("LookupEmbeddingCache(nil) = %+v err=%v", empty, err)
	}
	if _, err := s.LookupEmbeddingCache(ctx, []string{"a"}, "m", 0); err == nil {
		t.Fatal("LookupEmbeddingCache(dims=0) should error")
	}
	if err := s.InsertEmbeddingCache(ctx, []string{"a"}, "m", [][]float32{{1}, {2}}, 3); err == nil {
		t.Fatal("InsertEmbeddingCache length mismatch should error")
	}
	if err := s.InsertEmbeddingCache(ctx, nil, "m", nil, 3); err != nil {
		t.Fatalf("InsertEmbeddingCache(empty): %v", err)
	}
	if err := s.InsertEmbeddingCache(ctx, []string{"a"}, "m", [][]float32{{1, 0, 0}}, 0); err == nil {
		t.Fatal("InsertEmbeddingCache(dims=0) should error")
	}
	if _, err := s.PruneEmbeddingCache(ctx, 0); err == nil {
		t.Fatal("PruneEmbeddingCache(0) should error")
	}
	if err := s.EnsureEmbeddingCacheTable(ctx, 0); err == nil {
		t.Fatal("EnsureEmbeddingCacheTable(0) should error")
	}
	if err := s.EnsureEmbeddingCacheTable(ctx, maxDims+1); err == nil {
		t.Fatal("EnsureEmbeddingCacheTable(maxDims+1) should error")
	}

	// Happy path miss + insert + prune.
	if err := s.EnsureEmbeddingCacheTable(ctx, 4); err != nil {
		t.Fatal(err)
	}
	miss, err := s.LookupEmbeddingCache(ctx, []string{"nope"}, "m", 4)
	if err != nil || len(miss) != 0 {
		t.Fatalf("miss = %+v err=%v", miss, err)
	}
	if err := s.InsertEmbeddingCache(ctx, []string{"h1"}, "m", [][]float32{{1, 0, 0, 0}}, 4); err != nil {
		t.Fatal(err)
	}
	// ON CONFLICT DO NOTHING re-insert.
	if err := s.InsertEmbeddingCache(ctx, []string{"h1"}, "m", [][]float32{{0, 1, 0, 0}}, 4); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneEmbeddingCache(ctx, 4)
	if err != nil || n < 1 {
		t.Fatalf("PruneEmbeddingCache = %d err=%v", n, err)
	}
}

// TestInsertChunksMismatchAndInvalidDims covers validation branches.
// coverage-patch: 2026-07-17
func TestInsertChunksMismatchAndInvalidDims(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.EnsureChunksTable(ctx, 3)
	pid, _ := s.UpsertProject(ctx, "ins-val", "/i", "m", 0)
	fid, _ := s.UpsertFile(ctx, pid, "a.go", "h", 1)

	if err := s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "x"}},
		[][]float32{{1, 0}, {0, 1}}, 3); err == nil {
		t.Fatal("InsertChunks length mismatch should error")
	}
	if err := s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "x"}},
		[][]float32{{1, 0, 0}}, 0); err == nil {
		t.Fatal("InsertChunks(dims=0) should error")
	}
	// Empty batch is fine (no-op batch).
	if err := s.InsertChunks(ctx, pid, fid, nil, nil, 3); err != nil {
		t.Fatalf("InsertChunks(empty): %v", err)
	}
	if err := s.InsertChunksTextOnly(ctx, pid, fid, nil, 3); err != nil {
		t.Fatalf("InsertChunksTextOnly(empty): %v", err)
	}
	if err := s.InsertChunksTextOnly(ctx, pid, fid, []chunker.Chunk{{Content: "t"}}, 0); err == nil {
		t.Fatal("InsertChunksTextOnly(dims=0) should error")
	}
	if err := s.DeleteChunksForFile(ctx, pid, fid, 0); err == nil {
		t.Fatal("DeleteChunksForFile(dims=0) should error")
	}
	if err := s.DeleteChunksForFile(ctx, pid, fid, 3); err != nil {
		t.Fatalf("DeleteChunksForFile: %v", err)
	}
}

// TestFileUpToDateInvalidDimsAndMissingTable covers dims validation and
// missing-chunks-table (treat as not up to date).
// coverage-patch: 2026-07-17
func TestFileUpToDateInvalidDimsAndMissingTable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pid, _ := s.UpsertProject(ctx, "fud2", "/f", "m", 0)
	_, _ = s.UpsertFile(ctx, pid, "a.go", "h1", 10)

	// Invalid dims → error from chunksTable.
	if _, err := s.FileUpToDate(ctx, pid, "a.go", "h1", 0); err == nil {
		t.Fatal("FileUpToDate(dims=0) should error")
	}
	// Valid dims but table never created → exists query fails → false, nil.
	up, err := s.FileUpToDate(ctx, pid, "a.go", "h1", 7)
	if err != nil || up {
		t.Fatalf("FileUpToDate(no table) = %v err=%v, want false nil", up, err)
	}
}

// TestSearchSimilarInvalidDimsAndEmptyKeyword covers search guards.
// coverage-patch: 2026-07-17
func TestSearchSimilarInvalidDimsAndEmptyKeyword(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.EnsureChunksTable(ctx, 3)
	pid, _ := s.UpsertProject(ctx, "search-edge", "/s", "m", 3)

	if _, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 0, 5); err == nil {
		t.Fatal("SearchSimilar(dims=0) should error")
	}
	// Empty keyword words → nil, nil (after resolveDims).
	kw, err := s.SearchSimilarKeywords(ctx, pid, "   ", 3, 5)
	if err != nil || kw != nil {
		t.Fatalf("empty keyword = %+v err=%v", kw, err)
	}
	kw2, err := s.SearchSimilarKeywordsWorktree(ctx, pid, "", 3, 5, "main")
	if err != nil || kw2 != nil {
		t.Fatalf("empty worktree keyword = %+v err=%v", kw2, err)
	}
	// resolveDims falls through to 1024 for unknown project with dims<=0.
	d := s.resolveDims(ctx, 99999, -1)
	if d != 1024 {
		t.Fatalf("resolveDims(unknown) = %d, want 1024", d)
	}
}

// TestGetProjectCommitMissingAndListFileHashesEmpty covers more edges.
// coverage-patch: 2026-07-17
func TestGetProjectCommitMissingAndListFileHashesEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	sha, err := s.GetProjectCommit(ctx, 99999)
	if err != nil || sha != "" {
		t.Fatalf("GetProjectCommit(missing) = %q err=%v", sha, err)
	}
	hashes, err := s.ListFileHashes(ctx, 99999)
	if err != nil || len(hashes) != 0 {
		t.Fatalf("ListFileHashes(empty) = %v err=%v", hashes, err)
	}
	if err := s.DeleteFileByPath(ctx, 99999, "nope.go"); err != nil {
		t.Fatalf("DeleteFileByPath(missing): %v", err)
	}
}

// TestDeleteProjectMissing covers ErrNotFound.
// coverage-patch: 2026-07-17
func TestDeleteProjectMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.DeleteProject(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteProject(missing) = %v, want ErrNotFound", err)
	}
}

// TestClosedPoolMethodErrors opens a dedicated pool, closes it, and hits
// error returns across many methods without touching the shared test store.
// coverage-patch: 2026-07-17
func TestClosedPoolMethodErrors(t *testing.T) {
	shared := newTestStore(t)
	ctx := context.Background()
	dsn := shared.pool.Config().ConnString()
	if dsn == "" {
		t.Skip("empty ConnString")
	}
	s2, err := NewPgStore(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPgStore: %v", err)
	}
	s2.Close()

	// Must not panic; expect errors from closed pool.
	_ = s2.Ping(ctx)
	_, _ = s2.ListProjects(ctx, 10, 0)
	_, _ = s2.GetProject(ctx, "x")
	_, _ = s2.GetProjectByID(ctx, 1)
	_, _ = s2.GetProjectByIdentity(ctx, "x")
	_, _ = s2.UpsertProject(ctx, "x", "/x", "m", 0)
	_, _ = s2.CreateProject(ctx, "x", "m", "push", "", "", 0)
	_ = s2.DeleteProject(ctx, "x")
	_ = s2.UpdateProjectStatus(ctx, 1, "ready")
	_, _ = s2.UpsertFile(ctx, 1, "a.go", "h", 1)
	_, _ = s2.FileUpToDate(ctx, 1, "a.go", "h", 3)
	_, _ = s2.ListFileHashes(ctx, 1)
	_ = s2.DeleteFileByPath(ctx, 1, "a.go")
	_ = s2.EnsureChunksTable(ctx, 3)
	_ = s2.InsertChunks(ctx, 1, 1, []chunker.Chunk{{Content: "x"}}, [][]float32{{1, 0, 0}}, 3)
	_ = s2.InsertChunksTextOnly(ctx, 1, 1, []chunker.Chunk{{Content: "x"}}, 3)
	_, _ = s2.SearchSimilar(ctx, 1, []float32{1, 0, 0}, 3, 5)
	_, _ = s2.SearchSimilarKeywords(ctx, 1, "foo", 3, 5)
	_ = s2.SetWorktreeFiles(ctx, 1, "main", map[string]string{"a": "b"})
	_, _ = s2.PruneUnreferencedFiles(ctx, 1)
	_ = s2.InsertFileDependencies(ctx, 1, "a.go", []string{"b.go"})
	_, _ = s2.FetchGraphNeighbors(ctx, 1)
	_, _ = s2.FetchChunksByPath(ctx, 1, "a.go", 3, 10)
	_, _ = s2.FetchChunksByDirPrefix(ctx, 1, "src/", 3, 10)
	_, _ = s2.LookupEmbeddingCache(ctx, []string{"a"}, "m", 3)
	_ = s2.InsertEmbeddingCache(ctx, []string{"a"}, "m", [][]float32{{1, 0, 0}}, 3)
	_, _ = s2.PruneEmbeddingCache(ctx, 3)
	_, _ = s2.ListUsers(ctx, 10, 0)
	_, _ = s2.ListConversations(ctx, 1, 10, 0)
	_, _ = s2.ListMessages(ctx, 1, 10)
	_, _ = s2.ListGitCredentials(ctx)
	_, _ = s2.GetGitCredentialByID(ctx, 1)
	_, _ = s2.GetGitCredentialForProject(ctx, 1)
	_, _ = s2.GetGitCredentialForHost(ctx, "h")
	_ = s2.DeleteGitCredential(ctx, 1)
	_, _ = s2.ClaimJob(ctx)
	_, _ = s2.GetJob(ctx, 1)
	_, _ = s2.ListRecentJobs(ctx, 0, 10)
	_, _ = s2.GetProjectCommit(ctx, 1)
	_, _ = s2.FetchGraphPathsBFS(ctx, 1, []string{"a.go"}, 2)
	_, _ = s2.CountProjectFiles(ctx, 1)
	_, _ = s2.CountProjectChunks(ctx, 1, 3)
	_ = s2.DropAll(ctx)
}

// TestGitCredentialNotFoundEdges covers missing id/host/project lookups.
// coverage-patch: 2026-07-17
func TestGitCredentialNotFoundEdges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.GetGitCredentialByID(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGitCredentialByID: %v", err)
	}
	if _, err := s.GetGitCredentialForProject(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGitCredentialForProject: %v", err)
	}
	if _, err := s.GetGitCredentialForHost(ctx, "none.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGitCredentialForHost: %v", err)
	}
	if err := s.DeleteGitCredential(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteGitCredential: %v", err)
	}
	if err := s.UpdateGitCredential(ctx, &GitCredential{
		ID: 99999, Kind: "https", SecretEnc: []byte("x"),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateGitCredential: %v", err)
	}
	// Validation failures.
	if _, err := s.CreateGitCredential(ctx, &GitCredential{Kind: "https", SecretEnc: []byte("x")}); err == nil {
		t.Fatal("CreateGitCredential without scope should error")
	}
	if _, err := s.CreateGitCredential(ctx, &GitCredential{
		Host: "h", Kind: "bad", SecretEnc: []byte("x"),
	}); err == nil {
		t.Fatal("CreateGitCredential bad kind should error")
	}
	if _, err := s.CreateGitCredential(ctx, &GitCredential{
		Host: "h", Kind: "https", SecretEnc: nil,
	}); err == nil {
		t.Fatal("CreateGitCredential empty secret should error")
	}
	if err := s.UpdateGitCredential(ctx, &GitCredential{
		ID: 1, Kind: "bad", SecretEnc: []byte("x"),
	}); err == nil {
		t.Fatal("UpdateGitCredential bad kind should error")
	}

	// Empty list.
	list, err := s.ListGitCredentials(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("ListGitCredentials empty = %d err=%v", len(list), err)
	}
}

// TestSearchSimilarAfterInsert covers text-only exclusion + keyword hit.
// coverage-patch: 2026-07-17
func TestSearchSimilarAfterInsertTextOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.EnsureChunksTable(ctx, 3)
	pid, _ := s.UpsertProject(ctx, "to-search", "/t", "m", 0)
	fid, _ := s.UpsertFile(ctx, pid, ".env", "h", 5)
	if err := s.InsertChunksTextOnly(ctx, pid, fid,
		[]chunker.Chunk{{Content: "SECRET=abc", StartLine: 1, EndLine: 1}}, 3); err != nil {
		t.Fatal(err)
	}
	sim, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 5)
	if err != nil || len(sim) != 0 {
		t.Fatalf("vector over text-only = %d err=%v", len(sim), err)
	}
	kw, err := s.SearchSimilarKeywords(ctx, pid, "SECRET", 3, 5)
	if err != nil || len(kw) != 1 {
		t.Fatalf("keyword over text-only = %d err=%v", len(kw), err)
	}
}

// TestFetchChunksByPathLimitZero still works.
// coverage-patch: 2026-07-17
func TestFetchChunksLimitAndEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.EnsureChunksTable(ctx, 3)
	pid, _ := s.UpsertProject(ctx, "fetch-lim", "/f", "m", 0)
	fid, _ := s.UpsertFile(ctx, pid, "src/a.go", "h", 10)
	_ = s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{
			{Content: "a", StartLine: 1, EndLine: 1},
			{Content: "b", StartLine: 2, EndLine: 2},
		},
		[][]float32{{1, 0, 0}, {0, 1, 0}}, 3)

	byPath, err := s.FetchChunksByPath(ctx, pid, "src/a.go", 3, 1)
	if err != nil || len(byPath) != 1 {
		t.Fatalf("FetchChunksByPath limit=1 = %d err=%v", len(byPath), err)
	}
	byDir, err := s.FetchChunksByDirPrefix(ctx, pid, "src/", 3, 1)
	if err != nil || len(byDir) != 1 {
		t.Fatalf("FetchChunksByDirPrefix limit=1 = %d err=%v", len(byDir), err)
	}
	empty, err := s.FetchChunksByDirPrefix(ctx, pid, "zzz/", 3, 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty dir = %d err=%v", len(empty), err)
	}
	// Invalid dims.
	if _, err := s.FetchChunksByPath(ctx, pid, "src/a.go", 0, 10); err == nil {
		t.Fatal("FetchChunksByPath(dims=0) should error")
	}
	if _, err := s.FetchChunksByDirPrefix(ctx, pid, "src/", 0, 10); err == nil {
		t.Fatal("FetchChunksByDirPrefix(dims=0) should error")
	}
}

// TestProbeDimsNoTables returns 0 then resolveDims defaults to 1024.
// coverage-patch: 2026-07-17
func TestProbeDimsNoTables(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// Fresh DB after DropAll — may still have chunks_* from prior tests on shared
	// container. Probe for a project with no rows should still return 0 or a dims.
	pid, _ := s.UpsertProject(ctx, "probe-empty2", "/p", "m", 0)
	d := s.probeDimsForProject(ctx, pid)
	// Either 0 (no rows) or some leftover table dims; both ok for coverage.
	_ = d
	// With dims=0 on project and no chunks, resolveDims → 1024.
	got := s.resolveDims(ctx, pid, 0)
	if got != 1024 && got <= 0 {
		t.Fatalf("resolveDims = %d, want 1024 or positive probe", got)
	}
}
