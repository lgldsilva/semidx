package localstore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// TestLookupEmbeddingCacheTooManyHashes hits the max-990 guard.
// coverage-patch: 2026-07-17
func TestLookupEmbeddingCacheTooManyHashes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	_ = s.EnsureEmbeddingCacheTable(ctx, 3)

	hashes := make([]string, 991)
	for i := range hashes {
		hashes[i] = "h"
	}
	if _, err := s.LookupEmbeddingCache(ctx, hashes, "m", 3); err == nil {
		t.Fatal("LookupEmbeddingCache(991) should error")
	}
}

// TestSearchSimilarTopKZero uses the no-heap accumulate path (topK<=0).
// coverage-patch: 2026-07-17
func TestSearchSimilarTopKZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "topk0", "/tmp/t0", "bge-m3", 0)
	fid, _ := s.UpsertFile(ctx, pid, "a.go", "h", 10)
	_ = s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{
			{Content: "one", StartLine: 1, EndLine: 1},
			{Content: "two", StartLine: 2, EndLine: 2},
		},
		[][]float32{{1, 0, 0}, {0, 1, 0}}, 3)

	all, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 0)
	if err != nil {
		t.Fatalf("SearchSimilar(topK=0): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("SearchSimilar(topK=0) = %d results, want 2", len(all))
	}
	if all[0].Score < all[1].Score {
		t.Fatalf("results not sorted desc: %+v", all)
	}

	kw, err := s.SearchSimilarKeywords(ctx, pid, "one", 3, 0)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords(topK=0): %v", err)
	}
	if len(kw) < 1 {
		t.Fatalf("keyword topK=0 = %d, want >=1", len(kw))
	}
}

// TestListProjectsLimitOffset covers LIMIT > 0 branch.
// coverage-patch: 2026-07-17
func TestListProjectsLimitOffset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	_, _ = s.UpsertProject(ctx, "lp-a", "/a", "m", 0)
	_, _ = s.UpsertProject(ctx, "lp-b", "/b", "m", 0)
	_, _ = s.UpsertProject(ctx, "lp-c", "/c", "m", 0)

	limited, err := s.ListProjects(ctx, 2, 0)
	if err != nil || len(limited) != 2 {
		t.Fatalf("ListProjects(limit=2) = %d err=%v", len(limited), err)
	}
	off, err := s.ListProjects(ctx, 1, 2)
	if err != nil || len(off) != 1 {
		t.Fatalf("ListProjects(offset=2) = %d err=%v", len(off), err)
	}
}

// TestPruneOrphanEmbeddingsAndCache covers GC helpers after deletes.
// coverage-patch: 2026-07-17
func TestPruneOrphanEmbeddingsAndCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "orphan-gc", "/tmp/og", "bge-m3", 0)
	fid, _ := s.UpsertFile(ctx, pid, "a.go", "h", 10)
	_ = s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}},
		[][]float32{{1, 0, 0}}, 3)
	_ = s.EnsureEmbeddingCacheTable(ctx, 3)
	_ = s.InsertEmbeddingCache(ctx, []string{"ih"}, "bge-m3", [][]float32{{1, 0, 0}}, 3)

	if err := s.DeleteProject(ctx, "orphan-gc"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	n, err := s.PruneOrphanEmbeddings(ctx)
	if err != nil {
		t.Fatalf("PruneOrphanEmbeddings: %v", err)
	}
	if n < 1 {
		t.Fatalf("PruneOrphanEmbeddings = %d, want >=1", n)
	}
	n2, err := s.PruneOrphanEmbeddings(ctx)
	if err != nil || n2 != 0 {
		t.Fatalf("second PruneOrphanEmbeddings = %d err=%v", n2, err)
	}

	n3, err := s.PruneEmbeddingCache(ctx, 3)
	if err != nil || n3 < 1 {
		t.Fatalf("PruneEmbeddingCache = %d err=%v", n3, err)
	}
	n4, err := s.PruneEmbeddingCache(ctx, 3)
	if err != nil || n4 != 0 {
		t.Fatalf("second PruneEmbeddingCache = %d err=%v", n4, err)
	}
}

// TestGetProjectCommitMissing returns empty string for unknown id.
// coverage-patch: 2026-07-17
func TestGetProjectCommitMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	sha, err := s.GetProjectCommit(ctx, 99999)
	if err != nil || sha != "" {
		t.Fatalf("GetProjectCommit(missing) = %q err=%v, want empty", sha, err)
	}
}

// TestListConversationsLimitClamp covers limit<=0 and limit>200 defaults.
// coverage-patch: 2026-07-17
func TestListConversationsLimitClamp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	for i := 0; i < 3; i++ {
		if _, err := s.CreateConversation(ctx, 1, "p", "c"); err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
	}
	all, err := s.ListConversations(ctx, 1, 0, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListConversations(limit=0) = %d err=%v", len(all), err)
	}
	all2, err := s.ListConversations(ctx, 1, 500, 0)
	if err != nil || len(all2) != 3 {
		t.Fatalf("ListConversations(limit=500) = %d err=%v", len(all2), err)
	}
	page, err := s.ListConversations(ctx, 1, 1, 2)
	if err != nil || len(page) != 1 {
		t.Fatalf("ListConversations(limit=1,offset=2) = %d err=%v", len(page), err)
	}
}

// TestListMessagesLimits covers limit<=0 (all) and positive limit.
// coverage-patch: 2026-07-17
func TestListMessagesLimits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	c, err := s.CreateConversation(ctx, 0, "p", "t")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.AddMessage(ctx, c.ID, "user", "hi", ""); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}
	all, err := s.ListMessages(ctx, c.ID, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListMessages(0) = %d err=%v", len(all), err)
	}
	lim, err := s.ListMessages(ctx, c.ID, 2)
	if err != nil || len(lim) != 2 {
		t.Fatalf("ListMessages(2) = %d err=%v", len(lim), err)
	}
}

// TestSearchSimilarEmptyProject returns nil without error.
// coverage-patch: 2026-07-17
func TestSearchSimilarEmptyProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "empty-search", "/e", "m", 0)
	res, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 5)
	if err != nil || res != nil {
		t.Fatalf("empty SearchSimilar = %+v err=%v, want nil", res, err)
	}
	res2, err := s.SearchSimilarWorktree(ctx, pid, []float32{1, 0, 0}, 3, 5, "main")
	if err != nil || res2 != nil {
		t.Fatalf("empty worktree search = %+v err=%v", res2, err)
	}
}

// TestEnsureSchemaRebuildOldDB rebuilds a pre-F11 DB missing identity column.
// coverage-patch: 2026-07-17
func TestEnsureSchemaRebuildOldDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE projects (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		path TEXT,
		model TEXT,
		status TEXT
	)`); err != nil {
		t.Fatalf("create old projects: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (name, path, model, status) VALUES ('x','/x','m','ready')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	_ = db.Close()

	s, err := New(path)
	if err != nil {
		t.Fatalf("New(old schema): %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	id, err := s.UpsertProject(ctx, "fresh", "/f", "bge-m3", 0)
	if err != nil || id < 1 {
		t.Fatalf("UpsertProject after rebuild id=%d err=%v", id, err)
	}
}

// TestClosedStoreErrorPaths exercises many if-err-return branches after Close.
// coverage-patch: 2026-07-17
func TestClosedStoreErrorPaths(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "closed.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Close()
	s.Close()

	ctx := context.Background()
	if err := s.Ping(ctx); err == nil {
		t.Error("Ping after Close should fail")
	}
	if _, err := s.LookupEmbeddingCache(ctx, []string{"a"}, "m", 3); err == nil {
		t.Error("LookupEmbeddingCache after Close should fail")
	}
	if err := s.InsertEmbeddingCache(ctx, []string{"a"}, "m", [][]float32{{1}}, 1); err == nil {
		t.Error("InsertEmbeddingCache after Close should fail")
	}
	if _, err := s.PruneEmbeddingCache(ctx, 3); err == nil {
		t.Error("PruneEmbeddingCache after Close should fail")
	}
	if _, err := s.PruneOrphanEmbeddings(ctx); err == nil {
		t.Error("PruneOrphanEmbeddings after Close should fail")
	}
	if _, err := s.UpsertProject(ctx, "x", "/x", "m", 0); err == nil {
		t.Error("UpsertProject after Close should fail")
	}
	if _, err := s.CreateProject(ctx, "x", "m", "git", "", "", 0); err == nil {
		t.Error("CreateProject after Close should fail")
	}
	if _, err := s.GetProject(ctx, "x"); err == nil {
		t.Error("GetProject after Close should fail")
	}
	if _, err := s.ListProjects(ctx, 10, 0); err == nil {
		t.Error("ListProjects after Close should fail")
	}
	if err := s.DeleteProject(ctx, "x"); err == nil {
		t.Error("DeleteProject after Close should fail")
	}
	if _, err := s.UpsertFile(ctx, 1, "a.go", "h", 1); err == nil {
		t.Error("UpsertFile after Close should fail")
	}
	if _, err := s.FileUpToDate(ctx, 1, "a.go", "h", 3); err == nil {
		t.Error("FileUpToDate after Close should fail")
	}
	if _, err := s.ListFileHashes(ctx, 1); err == nil {
		t.Error("ListFileHashes after Close should fail")
	}
	if err := s.DeleteFileByPath(ctx, 1, "a.go"); err == nil {
		t.Error("DeleteFileByPath after Close should fail")
	}
	if err := s.InsertChunks(ctx, 1, 1, []chunker.Chunk{{Content: "x"}}, [][]float32{{1}}, 1); err == nil {
		t.Error("InsertChunks after Close should fail")
	}
	if _, err := s.SearchSimilar(ctx, 1, []float32{1}, 1, 5); err == nil {
		t.Error("SearchSimilar after Close should fail")
	}
	if _, err := s.SearchSimilarKeywords(ctx, 1, "foo", 1, 5); err == nil {
		t.Error("SearchSimilarKeywords after Close should fail")
	}
	if err := s.SetWorktreeFiles(ctx, 1, "main", map[string]string{"a": "b"}); err == nil {
		t.Error("SetWorktreeFiles after Close should fail")
	}
	if _, err := s.PruneUnreferencedFiles(ctx, 1); err == nil {
		t.Error("PruneUnreferencedFiles after Close should fail")
	}
	if err := s.InsertFileDependencies(ctx, 1, "a.go", []string{"b.go"}); err == nil {
		t.Error("InsertFileDependencies after Close should fail")
	}
	if _, err := s.FetchGraphNeighbors(ctx, 1); err == nil {
		t.Error("FetchGraphNeighbors after Close should fail")
	}
	if _, err := s.FetchChunksByPath(ctx, 1, "a.go", 3, 10); err == nil {
		t.Error("FetchChunksByPath after Close should fail")
	}
	if _, err := s.FetchChunksByDirPrefix(ctx, 1, "src/", 3, 10); err == nil {
		t.Error("FetchChunksByDirPrefix after Close should fail")
	}
	if err := s.DropAll(ctx); err == nil {
		t.Error("DropAll after Close should fail")
	}
	if _, err := s.ExportChunks(ctx, 1); err == nil {
		t.Error("ExportChunks after Close should fail")
	}
	if _, err := s.FetchGraphPathsBFS(ctx, 1, []string{"a.go"}, 2); err == nil {
		t.Error("FetchGraphPathsBFS after Close should fail")
	}
	if _, err := s.ListConversations(ctx, 0, 10, 0); err == nil {
		t.Error("ListConversations after Close should fail")
	}
	if _, err := s.ListMessages(ctx, 1, 10); err == nil {
		t.Error("ListMessages after Close should fail")
	}
}

// TestIsUniqueViolationNonSQLite returns false for plain errors.
// coverage-patch: 2026-07-17
func TestIsUniqueViolationNonSQLite(t *testing.T) {
	t.Parallel()
	if isUniqueViolation(nil) {
		t.Fatal("nil should not be unique violation")
	}
	if isUniqueViolation(sql.ErrNoRows) {
		t.Fatal("ErrNoRows should not be unique violation")
	}
	if isUniqueViolation(store.ErrNotFound) {
		t.Fatal("ErrNotFound should not be unique violation")
	}
}

// TestFetchGraphPathsBFSMaxDepthZero is a no-op.
// coverage-patch: 2026-07-17
func TestFetchGraphPathsBFSMaxDepthZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	pid, _ := s.UpsertProject(ctx, "bfs0", "/b", "m", 0)
	got, err := s.FetchGraphPathsBFS(ctx, pid, []string{"a.go"}, 0)
	if err != nil || got != nil {
		t.Fatalf("maxDepth=0 = %+v err=%v", got, err)
	}
}

// TestInsertChunksTextOnlyThenSearch covers empty text-only batch + export.
// coverage-patch: 2026-07-17
func TestInsertChunksTextOnlyThenSearch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "text-only2", "/t", "m", 0)
	fid, _ := s.UpsertFile(ctx, pid, ".env", "h", 5)
	if err := s.InsertChunksTextOnly(ctx, pid, fid,
		[]chunker.Chunk{{Content: "KEY=val", StartLine: 1, EndLine: 1}}, 3); err != nil {
		t.Fatalf("InsertChunksTextOnly: %v", err)
	}
	if err := s.InsertChunksTextOnly(ctx, pid, fid, nil, 3); err != nil {
		t.Fatalf("InsertChunksTextOnly(nil): %v", err)
	}
	exp, err := s.ExportChunks(ctx, pid)
	if err != nil || len(exp) != 1 || exp[0].Embedding != nil {
		t.Fatalf("ExportChunks text-only = %+v err=%v", exp, err)
	}
}

// TestNewRejectsFileAsDirectory when parent path is a file.
// coverage-patch: 2026-07-17
func TestNewRejectsFileAsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "notadir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(filepath.Join(blocker, "index.db")); err == nil {
		t.Fatal("New under file path should fail")
	}
}

// TestEnsureSchemaMissingLastIndexedCommit rebuilds when column is missing.
// coverage-patch: 2026-07-17
func TestEnsureSchemaMissingLastIndexedCommit(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mid.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Has identity but no last_indexed_commit → rebuild.
	if _, err := db.Exec(`CREATE TABLE projects (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		path TEXT,
		model TEXT,
		status TEXT,
		source_type TEXT,
		git_url TEXT,
		branch TEXT,
		identity TEXT NOT NULL UNIQUE,
		dims INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.UpsertProject(ctx, "ok", "/ok", "m", 0); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}
