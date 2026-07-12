package localstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// TestCountProjectFiles verifies CountProjectFiles returns accurate counts
// and zero for empty projects.
func TestCountProjectFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "count-files", "/tmp/c", "bge-m3", 0)

	// No files yet.
	if n, err := s.CountProjectFiles(ctx, projectID); err != nil || n != 0 {
		t.Fatalf("CountProjectFiles(empty) = %d, err %v; want 0", n, err)
	}

	_, _ = s.UpsertFile(ctx, projectID, "a.go", "h1", 10)
	_, _ = s.UpsertFile(ctx, projectID, "b.go", "h2", 20)

	if n, err := s.CountProjectFiles(ctx, projectID); err != nil || n != 2 {
		t.Fatalf("CountProjectFiles = %d, err %v; want 2", n, err)
	}

	// Unknown project returns 0 (COUNT(*) always returns a row).
	if n, err := s.CountProjectFiles(ctx, 99999); err != nil || n != 0 {
		t.Fatalf("CountProjectFiles(unknown) = %d, err %v; want 0", n, err)
	}
}

// TestInsertFileDependenciesAndFetchGraph covers the dependency graph
// round-trip: insert deps, fetch them, replace them, clear them.
func TestInsertFileDependenciesAndFetchGraph(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "dep-graph", "/tmp/dep", "bge-m3", 0)

	// Insert dependencies.
	if err := s.InsertFileDependencies(ctx, projectID, "main.go", []string{"util.go", "config.go"}); err != nil {
		t.Fatalf("InsertFileDependencies: %v", err)
	}

	graph, err := s.FetchGraphNeighbors(ctx, projectID)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors: %v", err)
	}
	if len(graph) != 1 || len(graph["main.go"]) != 2 {
		t.Errorf("graph = %+v, want 1 source with 2 targets", graph)
	}

	// Replace deps for same source — old edges are deleted, new ones inserted.
	if err := s.InsertFileDependencies(ctx, projectID, "main.go", []string{"util.go"}); err != nil {
		t.Fatalf("InsertFileDependencies(replace): %v", err)
	}
	graph, _ = s.FetchGraphNeighbors(ctx, projectID)
	if len(graph["main.go"]) != 1 || graph["main.go"][0] != "util.go" {
		t.Errorf("after replace: %v", graph["main.go"])
	}

	// Empty targets clears all deps for that source file.
	if err := s.InsertFileDependencies(ctx, projectID, "main.go", nil); err != nil {
		t.Fatalf("InsertFileDependencies(empty): %v", err)
	}
	graph, _ = s.FetchGraphNeighbors(ctx, projectID)
	if _, ok := graph["main.go"]; ok {
		t.Errorf("main.go still has deps after clearing: %v", graph)
	}

	// Unknown project returns empty map.
	empty, err := s.FetchGraphNeighbors(ctx, 99999)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors(unknown): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("unknown project graph = %v, want empty", empty)
	}
}

// TestFetchChunksByPathAndDirPrefix covers FetchChunksByPath and
// FetchChunksByDirPrefix including limit and no-match edge cases.
func TestFetchChunksByPathAndDirPrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "chunk-fetch", "/tmp/cf", "bge-m3", 3)
	_ = s.EnsureChunksTable(ctx, 3)

	fid1, _ := s.UpsertFile(ctx, projectID, "src/a.go", "h1", 10)
	fid2, _ := s.UpsertFile(ctx, projectID, "src/b.go", "h2", 10)
	fid3, _ := s.UpsertFile(ctx, projectID, "test/a_test.go", "h3", 10)

	chunks1 := []chunker.Chunk{
		{Content: "package a", StartLine: 1, EndLine: 1},
		{Content: "func A() {}", StartLine: 3, EndLine: 5},
	}
	chunks2 := []chunker.Chunk{{Content: "package b", StartLine: 1, EndLine: 1}}

	_ = s.InsertChunks(ctx, projectID, fid1, chunks1, [][]float32{{1, 0, 0}, {0, 1, 0}}, 3)
	_ = s.InsertChunks(ctx, projectID, fid2, chunks2, [][]float32{{0, 0, 1}}, 3)
	_ = s.InsertChunks(ctx, projectID, fid3, nil, nil, 3) // text-only, no embedding

	// Fetch by path.
	byPath, err := s.FetchChunksByPath(ctx, projectID, "src/a.go", 3, 10)
	if err != nil {
		t.Fatalf("FetchChunksByPath: %v", err)
	}
	if len(byPath) != 2 {
		t.Errorf("FetchChunksByPath returned %d, want 2", len(byPath))
	}
	if byPath[0].Content != "package a" || byPath[1].Content != "func A() {}" {
		t.Errorf("chunks = %+v", byPath)
	}

	// Limit caps the results.
	byPathLimit, err := s.FetchChunksByPath(ctx, projectID, "src/a.go", 3, 1)
	if err != nil || len(byPathLimit) != 1 {
		t.Errorf("FetchChunksByPath(limit=1) = %d err=%v, want 1", len(byPathLimit), err)
	}

	// Unknown path.
	empty, err := s.FetchChunksByPath(ctx, projectID, "nope.go", 3, 10)
	if err != nil || len(empty) != 0 {
		t.Errorf("FetchChunksByPath(unknown) = %d err=%v, want 0", len(empty), err)
	}

	// Fetch by dir prefix.
	byDir, err := s.FetchChunksByDirPrefix(ctx, projectID, "src/", 3, 10)
	if err != nil {
		t.Fatalf("FetchChunksByDirPrefix: %v", err)
	}
	if len(byDir) != 3 { // a.go has 2, b.go has 1
		t.Errorf("FetchChunksByDirPrefix(src/) = %d, want 3", len(byDir))
	}

	// Dir prefix with limit.
	byDirLimit, err := s.FetchChunksByDirPrefix(ctx, projectID, "src/", 3, 2)
	if err != nil || len(byDirLimit) != 2 {
		t.Errorf("FetchChunksByDirPrefix(limit=2) = %d err=%v, want 2", len(byDirLimit), err)
	}

	// No-match dir prefix.
	emptyDir, err := s.FetchChunksByDirPrefix(ctx, projectID, "nonexistent/", 3, 10)
	if err != nil || len(emptyDir) != 0 {
		t.Errorf("FetchChunksByDirPrefix(nonexistent) = %d err=%v, want 0", len(emptyDir), err)
	}
}

// TestExportChunks verifies ExportChunks returns all chunks with their
// file metadata, including text-only (nil embedding) chunks.
func TestExportChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	pid, _ := s.UpsertProject(ctx, "export-me", "/tmp/exp", "bge-m3", 3)
	_ = s.EnsureChunksTable(ctx, 3)

	fid1, _ := s.UpsertFile(ctx, pid, "code.go", "h1", 100)
	fid2, _ := s.UpsertFile(ctx, pid, ".env", "h2", 20)

	// One embedded chunk and one text-only chunk.
	_ = s.InsertChunks(ctx, pid, fid1, []chunker.Chunk{{Content: "func hello()", StartLine: 1, EndLine: 3}}, [][]float32{{1, 0, 0}}, 3)
	_ = s.InsertChunksTextOnly(ctx, pid, fid2, []chunker.Chunk{{Content: "SECRET=token", StartLine: 1, EndLine: 1}}, 3)

	exported, err := s.ExportChunks(ctx, pid)
	if err != nil {
		t.Fatalf("ExportChunks: %v", err)
	}
	if len(exported) != 2 {
		t.Fatalf("ExportChunks returned %d rows, want 2", len(exported))
	}

	var codeChunk, envChunk *ExportedChunk
	for i, ec := range exported {
		switch ec.FilePath {
		case "code.go":
			codeChunk = &exported[i]
		case ".env":
			envChunk = &exported[i]
		}
	}
	if codeChunk == nil || envChunk == nil {
		t.Fatalf("ExportChunks missing entries: code=%v env=%v", codeChunk != nil, envChunk != nil)
	}
	if codeChunk.FileHash != "h1" || codeChunk.FileSize != 100 {
		t.Errorf("code chunk metadata = %+v", codeChunk)
	}
	if codeChunk.Embedding == nil || len(codeChunk.Embedding) != 3 {
		t.Errorf("code chunk embedding = %v, want 3 floats", codeChunk.Embedding)
	}
	if codeChunk.StartLine != 1 || codeChunk.EndLine != 3 {
		t.Errorf("code chunk line range = [%d,%d]", codeChunk.StartLine, codeChunk.EndLine)
	}
	if envChunk.Embedding != nil {
		t.Errorf("text-only chunk should have nil embedding, got %v", envChunk.Embedding)
	}
	if envChunk.Content != "SECRET=token" {
		t.Errorf("env chunk content = %q", envChunk.Content)
	}

	// Unknown project returns empty.
	empty, err := s.ExportChunks(ctx, 99999)
	if err != nil || len(empty) != 0 {
		t.Errorf("ExportChunks(unknown) = %d err=%v, want 0", len(empty), err)
	}
}

// TestPruneUnreferencedFilesExhaustive exercises the prune flow end-to-end:
// set worktree files with some hashes, insert extra files not in worktree,
// verify they get pruned, and check the cascade deletes file_dependencies.
func TestPruneUnreferencedFilesExhaustive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "prune-full", "/tmp/pf", "bge-m3", 0)

	// Insert files and a dependency.
	_, _ = s.UpsertFile(ctx, projectID, "kept.go", "h1", 10)
	_, _ = s.UpsertFile(ctx, projectID, "pruned.go", "h2", 20) // will be pruned
	_ = s.InsertFileDependencies(ctx, projectID, "pruned.go", []string{"kept.go"})

	// Set worktree with only kept.go.
	if err := s.SetWorktreeFiles(ctx, projectID, "main", map[string]string{"kept.go": "h1"}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}

	n, err := s.PruneUnreferencedFiles(ctx, projectID)
	if err != nil {
		t.Fatalf("PruneUnreferencedFiles: %v", err)
	}
	// Should remove pruned.go (not in any worktree) and also its dependencies
	// via the cleanup. That counts as 1 file removed.
	if n != 1 {
		t.Errorf("PruneUnreferencedFiles removed %d, want 1", n)
	}

	// Verify kept.go still exists.
	hashes, err := s.ListFileHashes(ctx, projectID)
	if err != nil || len(hashes) != 1 || hashes["kept.go"] != "h1" {
		t.Errorf("after prune hashes = %v err=%v, want {kept.go: h1}", hashes, err)
	}

	// Verify the dependency's source file is gone too.
	graph, err := s.FetchGraphNeighbors(ctx, projectID)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors after prune: %v", err)
	}
	if _, ok := graph["pruned.go"]; ok {
		t.Errorf("dependencies for pruned file still in graph: %v", graph)
	}
}

// TestEnsureProjectIdentityDocsPath tests the docs path variant of
// EnsureProjectIdentity (sourceType="docs").
func TestEnsureProjectIdentityDocsPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.EnsureProjectIdentity(ctx, "path:/docs/my-project", "my-project", "/abs/path/docs/my-project", "bge-m3", "docs", 0)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity(docs): %v", err)
	}

	p, err := s.GetProjectByIdentity(ctx, "path:/docs/my-project")
	if err != nil {
		t.Fatalf("GetProjectByIdentity: %v", err)
	}
	if p.SourceType != "docs" || p.Name != "my-project" {
		t.Errorf("docs project = %+v", p)
	}

	// Re-upsert is idempotent.
	id2, _ := s.EnsureProjectIdentity(ctx, "path:/docs/my-project", "my-project", "/abs/path/docs/my-project", "bge-m3", "docs", 0)
	if id2 != id {
		t.Errorf("re-upsert changed id %d -> %d", id, id2)
	}
}

// TestGetProjectByIdentityNotFound covers the ErrNotFound path.
func TestGetProjectByIdentityNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.GetProjectByIdentity(ctx, "nope"); err != store.ErrNotFound {
		t.Errorf("GetProjectByIdentity(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestSearchSimilarWorktree verifies worktree-scoped vector search.
func TestSearchSimilarWorktree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "wt-vec", "/tmp/wv", "bge-m3", 3)
	fid1, _ := s.UpsertFile(ctx, projectID, "v1.go", "hash1", 10)
	fid2, _ := s.UpsertFile(ctx, projectID, "v2.go", "hash2", 10)

	_ = s.InsertChunks(ctx, projectID, fid1, []chunker.Chunk{{Content: "alpha", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0, 0}}, 3)
	_ = s.InsertChunks(ctx, projectID, fid2, []chunker.Chunk{{Content: "beta", StartLine: 2, EndLine: 2}}, [][]float32{{0, 1, 0}}, 3)

	// Worktree with only v1.go.
	if err := s.SetWorktreeFiles(ctx, projectID, "main", map[string]string{"v1.go": "hash1"}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}

	results, err := s.SearchSimilarWorktree(ctx, projectID, []float32{1, 0, 0}, 3, 10, "main")
	if err != nil {
		t.Fatalf("SearchSimilarWorktree: %v", err)
	}
	if len(results) != 1 || results[0].Content != "alpha" {
		t.Errorf("worktree search = %+v, want alpha only", results)
	}

	// Unrestricted returns both.
	all, _ := s.SearchSimilar(ctx, projectID, []float32{1, 0, 0}, 3, 10)
	if len(all) != 2 {
		t.Errorf("unrestricted search = %d, want 2", len(all))
	}
}

// TestSearchSimilarKeywordsWorktree verifies worktree-scoped keyword search.
func TestSearchSimilarKeywordsWorktree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "kw-wt", "/tmp/kwt", "bge-m3", 3)
	fid1, _ := s.UpsertFile(ctx, projectID, "auth.go", "h1", 10)
	fid2, _ := s.UpsertFile(ctx, projectID, "other.go", "h2", 10)

	_ = s.InsertChunks(ctx, projectID, fid1, []chunker.Chunk{{Content: "authenticate user", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0, 0}}, 3)
	_ = s.InsertChunks(ctx, projectID, fid2, []chunker.Chunk{{Content: "authenticate admin", StartLine: 2, EndLine: 2}}, [][]float32{{0, 1, 0}}, 3)

	if err := s.SetWorktreeFiles(ctx, projectID, "main", map[string]string{"auth.go": "h1"}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}

	// Worktree-scoped keyword returns only auth.go.
	kw, err := s.SearchSimilarKeywordsWorktree(ctx, projectID, "authenticate", 3, 10, "main")
	if err != nil {
		t.Fatalf("SearchSimilarKeywordsWorktree: %v", err)
	}
	if len(kw) != 1 || kw[0].FilePath != "auth.go" {
		t.Errorf("worktree keyword = %+v, want auth.go only", kw)
	}

	// Unrestricted returns both.
	all, _ := s.SearchSimilarKeywords(ctx, projectID, "authenticate", 3, 10)
	if len(all) != 2 {
		t.Errorf("unrestricted keyword = %d, want 2", len(all))
	}
}

// TestNewWithNonExistentDir validates New creates the parent directory when
// it does not exist.
func TestNewWithNonExistentDir(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "deep", "nested", "index.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New(non-existent dir) = %v", err)
	}
	s.Close()
}

// TestSetWorktreeFilesEmptyBatch verifies that SetWorktreeFiles with an
// empty file map does not error (edge case).
func TestSetWorktreeFilesEmptyBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "empty-wt", "/tmp/ew", "bge-m3", 0)
	if err := s.SetWorktreeFiles(ctx, projectID, "main", nil); err != nil {
		t.Fatalf("SetWorktreeFiles(nil): %v", err)
	}
	if err := s.SetWorktreeFiles(ctx, projectID, "main", map[string]string{}); err != nil {
		t.Fatalf("SetWorktreeFiles(empty): %v", err)
	}
}

// TestSearchKeywordsEmptyQuery checks that an empty or whitespace-only
// query returns (nil, nil) without error.
func TestSearchKeywordsEmptyQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "kw-empty", "/tmp/ke", "bge-m3", 0)

	for _, q := range []string{"", "   ", "\t"} {
		results, err := s.SearchSimilarKeywords(ctx, projectID, q, 3, 5)
		if err != nil {
			t.Fatalf("SearchSimilarKeywords(%q): %v", q, err)
		}
		if results != nil {
			t.Errorf("SearchSimilarKeywords(%q) = %+v, want nil", q, results)
		}
		// Same for worktree variant.
		wtResults, err := s.SearchSimilarKeywordsWorktree(ctx, projectID, q, 3, 5, "main")
		if err != nil || wtResults != nil {
			t.Errorf("SearchSimilarKeywordsWorktree(%q) = %+v err=%v, want nil", q, wtResults, err)
		}
	}
}

func TestProjectCommitRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "commit", "/tmp/cmt", "bge-m3", 0)
	if sha, err := s.GetProjectCommit(ctx, projectID); err != nil || sha != "" {
		t.Fatalf("initial commit = %q err=%v", sha, err)
	}
	if err := s.UpdateProjectCommit(ctx, projectID, "deadbeef"); err != nil {
		t.Fatalf("UpdateProjectCommit: %v", err)
	}
	if sha, err := s.GetProjectCommit(ctx, projectID); err != nil || sha != "deadbeef" {
		t.Fatalf("commit = %q err=%v", sha, err)
	}
}

func TestFetchGraphPathsBFS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "bfs", "/tmp/bfs", "bge-m3", 0)
	if err := s.InsertFileDependencies(ctx, projectID, "main.go", []string{"util.go"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertFileDependencies(ctx, projectID, "util.go", []string{"config.go"}); err != nil {
		t.Fatal(err)
	}

	paths, err := s.FetchGraphPathsBFS(ctx, projectID, []string{"main.go"}, 2)
	if err != nil {
		t.Fatalf("FetchGraphPathsBFS: %v", err)
	}
	if len(paths) < 2 {
		t.Fatalf("paths = %+v, want neighbors at depth 1+", paths)
	}
	if paths["util.go"] != 1 {
		t.Errorf("util.go depth = %d, want 1", paths["util.go"])
	}

	if got, err := s.FetchGraphPathsBFS(ctx, projectID, nil, 2); err != nil || got != nil {
		t.Fatalf("empty seeds = %+v err=%v", got, err)
	}
}

func TestEmbeddingCacheRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.EnsureEmbeddingCacheTable(ctx, 3); err != nil {
		t.Fatal(err)
	}
	hashes := []string{"hash-a", "hash-b"}
	embs := [][]float32{{1, 0, 0}, {0, 1, 0}}
	if err := s.InsertEmbeddingCache(ctx, hashes, "bge-m3", embs, 3); err != nil {
		t.Fatalf("InsertEmbeddingCache: %v", err)
	}
	got, err := s.LookupEmbeddingCache(ctx, hashes, "bge-m3", 3)
	if err != nil {
		t.Fatalf("LookupEmbeddingCache: %v", err)
	}
	if len(got) != 2 || len(got["hash-a"]) != 3 {
		t.Fatalf("cache = %+v", got)
	}
	n, err := s.PruneEmbeddingCache(ctx, 3)
	if err != nil || n != 2 {
		t.Fatalf("PruneEmbeddingCache = %d err=%v", n, err)
	}
}
