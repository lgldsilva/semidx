package localstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// newTestStore opens a SQLiteStore backed by a fresh temp-file DB (no
// testcontainers — SQLite is in-process).
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestLifecycle exercises the full index/search path against a temp DB:
// create project, upsert file, insert embedded chunks, then rank them by cosine.
func TestLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}

	projectID, err := s.UpsertProject(ctx, "demo", "/tmp/demo", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	// UpsertProject is idempotent on name and must return the same id.
	if again, err := s.UpsertProject(ctx, "demo", "/tmp/demo2", "bge-m3", 0); err != nil || again != projectID {
		t.Fatalf("UpsertProject re-run: id=%d err=%v (want id=%d)", again, err, projectID)
	}

	got, err := s.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != projectID || got.Model != "bge-m3" || got.Path != "/tmp/demo2" {
		t.Fatalf("GetProject = %+v", got)
	}
	if byID, err := s.GetProjectByID(ctx, projectID); err != nil || byID.Name != "demo" {
		t.Fatalf("GetProjectByID = %+v err=%v", byID, err)
	}

	fileID, err := s.UpsertFile(ctx, projectID, "main.go", "hash-v1", 100)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	chunks := []chunker.Chunk{
		{Content: "package main", StartLine: 1, EndLine: 1},
		{Content: "func main() {}", StartLine: 3, EndLine: 5},
	}
	embeddings := [][]float32{
		{1, 0, 0},
		{0, 1, 0},
	}
	if err := s.InsertChunks(ctx, projectID, fileID, chunks, embeddings, 3); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	// A query aligned with the first chunk must rank it first with score ~1.
	results, err := s.SearchSimilar(ctx, projectID, []float32{1, 0, 0}, 3, 5)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("SearchSimilar returned %d results, want 2", len(results))
	}
	if results[0].Content != "package main" {
		t.Fatalf("top result = %q, want %q", results[0].Content, "package main")
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf("results not ranked by score: %v", results)
	}
	if results[0].Score < 0.99 {
		t.Fatalf("aligned score = %f, want ~1.0", results[0].Score)
	}
	if results[0].StartLine != 1 || results[0].EndLine != 1 {
		t.Fatalf("line range = %d-%d, want 1-1", results[0].StartLine, results[0].EndLine)
	}

	// topK caps the number of hits.
	if top1, err := s.SearchSimilar(ctx, projectID, []float32{1, 0, 0}, 3, 1); err != nil || len(top1) != 1 {
		t.Fatalf("SearchSimilar topK=1 returned %d err=%v", len(top1), err)
	}
}

func TestKeywordSearch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	projectID, err := s.UpsertProject(ctx, "kw", "/tmp/kw", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	fileID, err := s.UpsertFile(ctx, projectID, "handler.go", "h1", 50)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	chunks := []chunker.Chunk{
		{Content: "func handleLogin(w http.ResponseWriter)", StartLine: 10, EndLine: 12},
		{Content: "func computeChecksum(b []byte)", StartLine: 20, EndLine: 22},
	}
	if err := s.InsertChunks(ctx, projectID, fileID, chunks, [][]float32{{1, 0}, {0, 1}}, 2); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	results, err := s.SearchSimilarKeywords(ctx, projectID, "handleLogin", 2, 5)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords: %v", err)
	}
	if len(results) != 1 || results[0].StartLine != 10 {
		t.Fatalf("keyword search = %+v, want the handleLogin chunk", results)
	}
	if results[0].Score != 0.5 {
		t.Fatalf("keyword score = %f, want 0.5", results[0].Score)
	}

	// An empty query yields nothing.
	if empty, err := s.SearchSimilarKeywords(ctx, projectID, "   ", 2, 5); err != nil || empty != nil {
		t.Fatalf("empty query = %+v err=%v, want nil", empty, err)
	}
}

// TestTextOnlyExcludedFromVectorSearch verifies NULL-embedding chunks are
// keyword-searchable but never surface via SearchSimilar.
func TestTextOnlyExcludedFromVectorSearch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "sensitive", "/tmp/s", "bge-m3", 0)
	fileID, _ := s.UpsertFile(ctx, projectID, ".env", "e1", 20)
	chunks := []chunker.Chunk{{Content: "SECRET=topsecret", StartLine: 1, EndLine: 1}}
	if err := s.InsertChunksTextOnly(ctx, projectID, fileID, chunks, 3); err != nil {
		t.Fatalf("InsertChunksTextOnly: %v", err)
	}

	if sim, err := s.SearchSimilar(ctx, projectID, []float32{1, 0, 0}, 3, 5); err != nil || len(sim) != 0 {
		t.Fatalf("SearchSimilar over text-only = %d results err=%v, want 0", len(sim), err)
	}
	if kw, err := s.SearchSimilarKeywords(ctx, projectID, "SECRET", 3, 5); err != nil || len(kw) != 1 {
		t.Fatalf("keyword search over text-only = %d results err=%v, want 1", len(kw), err)
	}
}

// TestIncrementalFileUpToDate covers the indexer's skip-unchanged fast path.
// Assertions are split into helpers so the body stays under the cognitive-
// complexity gate (SonarQube).
func TestIncrementalFileUpToDate(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	projectID, _ := s.UpsertProject(ctx, "inc", "/tmp/inc", "bge-m3", 0)

	assertFileNotUpToDate(t, s, ctx, projectID, "a.go", "h1", 2, "unknown")

	fileID, _ := s.UpsertFile(ctx, projectID, "a.go", "h1", 10)
	assertFileNotUpToDate(t, s, ctx, projectID, "a.go", "h1", 2, "no chunks")

	if err := s.InsertChunks(ctx, projectID, fileID, []chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0}}, 2); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	assertFileUpToDate(t, s, ctx, projectID, "a.go", "h1", 2)
	// Dims-aware: chunks at dims=2 must not skip a dims=3 re-index.
	assertFileNotUpToDate(t, s, ctx, projectID, "a.go", "h1", 3, "other dims")
	assertFileNotUpToDate(t, s, ctx, projectID, "a.go", "h2", 2, "changed")
	assertListFileHashes(t, s, ctx, projectID)
}

func assertFileNotUpToDate(t *testing.T, s *SQLiteStore, ctx context.Context, projectID int, path, hash string, dims int, label string) {
	t.Helper()
	up, err := s.FileUpToDate(ctx, projectID, path, hash, dims)
	if err != nil || up {
		t.Fatalf("FileUpToDate(%s) = %v err=%v, want false", label, up, err)
	}
}

func assertFileUpToDate(t *testing.T, s *SQLiteStore, ctx context.Context, projectID int, path, hash string, dims int) {
	t.Helper()
	up, err := s.FileUpToDate(ctx, projectID, path, hash, dims)
	if err != nil || !up {
		t.Fatalf("FileUpToDate(indexed) = %v err=%v, want true", up, err)
	}
}

func assertListFileHashes(t *testing.T, s *SQLiteStore, ctx context.Context, projectID int) {
	t.Helper()
	hashes, err := s.ListFileHashes(ctx, projectID)
	if err != nil || hashes["a.go"] != "h1" {
		t.Fatalf("ListFileHashes = %v err=%v", hashes, err)
	}
	infos, err := s.ListFileHashesWithTime(ctx, projectID)
	if err != nil {
		t.Fatalf("ListFileHashesWithTime: %v", err)
	}
	if infos["a.go"].Hash != "h1" {
		t.Fatalf("ListFileHashesWithTime hash = %q", infos["a.go"].Hash)
	}
	if infos["a.go"].IndexedAt.IsZero() {
		t.Fatal("ListFileHashesWithTime IndexedAt should be set")
	}
}

func TestCreateProjectAndDuplicate(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	p, err := s.CreateProject(ctx, "git-proj", "bge-m3", "git", "https://example.com/r.git", "main", 0)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Status != "registered" || p.SourceType != "git" || p.GitURL == "" || p.Branch != "main" {
		t.Fatalf("CreateProject = %+v", p)
	}

	// A duplicate name maps to the shared sentinel.
	if _, err := s.CreateProject(ctx, "git-proj", "bge-m3", "git", "", "", 0); err != store.ErrProjectExists {
		t.Fatalf("duplicate CreateProject err = %v, want ErrProjectExists", err)
	}

	list, err := s.ListProjects(ctx, 0, 0)
	if err != nil || len(list) != 1 || list[0].Name != "git-proj" {
		t.Fatalf("ListProjects = %+v err=%v", list, err)
	}
}

func TestDeleteAndCascade(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "del", "/tmp/del", "bge-m3", 0)
	fileID, _ := s.UpsertFile(ctx, projectID, "a.go", "h1", 10)
	if err := s.InsertChunks(ctx, projectID, fileID, []chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0}}, 2); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	// DeleteChunksForFile drops only the chunks.
	if err := s.DeleteChunksForFile(ctx, projectID, fileID, 2); err != nil {
		t.Fatalf("DeleteChunksForFile: %v", err)
	}
	if sim, err := s.SearchSimilar(ctx, projectID, []float32{1, 0}, 2, 5); err != nil || len(sim) != 0 {
		t.Fatalf("after DeleteChunksForFile: %d results err=%v, want 0", len(sim), err)
	}

	// Re-insert, then DeleteFileByPath cascades to chunks.
	if err := s.InsertChunks(ctx, projectID, fileID, []chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0}}, 2); err != nil {
		t.Fatalf("re-InsertChunks: %v", err)
	}
	if err := s.DeleteFileByPath(ctx, projectID, "a.go"); err != nil {
		t.Fatalf("DeleteFileByPath: %v", err)
	}
	if hashes, err := s.ListFileHashes(ctx, projectID); err != nil || len(hashes) != 0 {
		t.Fatalf("after DeleteFileByPath: hashes=%v err=%v", hashes, err)
	}
	if sim, err := s.SearchSimilar(ctx, projectID, []float32{1, 0}, 2, 5); err != nil || len(sim) != 0 {
		t.Fatalf("chunks not cascaded on file delete: %d results err=%v", len(sim), err)
	}

	// DeleteProject on a missing name → ErrNotFound; on an existing one → nil.
	if err := s.DeleteProject(ctx, "nope"); err != store.ErrNotFound {
		t.Fatalf("DeleteProject(missing) err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteProject(ctx, "del"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := s.GetProject(ctx, "del"); err != store.ErrNotFound {
		t.Fatalf("GetProject after delete err = %v, want ErrNotFound", err)
	}
}

func TestUpdateProjectStatus(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "st", "/tmp/st", "bge-m3", 0)
	if err := s.UpdateProjectStatus(ctx, projectID, "ready"); err != nil {
		t.Fatalf("UpdateProjectStatus: %v", err)
	}
	p, _ := s.GetProject(ctx, "st")
	if p.Status != "ready" {
		t.Fatalf("status = %q, want ready", p.Status)
	}
}

func TestDropAll(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	projectID, _ := s.UpsertProject(ctx, "d1", "/tmp/d1", "bge-m3", 0)
	fileID, _ := s.UpsertFile(ctx, projectID, "a.go", "h1", 10)
	if err := s.InsertChunks(ctx, projectID, fileID, []chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0}}, 2); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	if err := s.DropAll(ctx); err != nil {
		t.Fatalf("DropAll: %v", err)
	}
	if list, err := s.ListProjects(ctx, 0, 0); err != nil || len(list) != 0 {
		t.Fatalf("after DropAll: %d projects err=%v, want 0", len(list), err)
	}

	// Identity is reset: the next project gets id 1 again.
	newID, _ := s.UpsertProject(ctx, "d2", "/tmp/d2", "bge-m3", 0)
	if newID != 1 {
		t.Fatalf("post-DropAll id = %d, want 1 (identity reset)", newID)
	}
}

// TestEncodeDecodeRoundTrip guards the float32 BLOB codec.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	vec := []float32{0, 1.5, -2.25, 3.125e10, -0.0009765625}
	got := decodeEmbedding(encodeEmbedding(vec))
	if len(got) != len(vec) {
		t.Fatalf("len = %d, want %d", len(got), len(vec))
	}
	for i := range vec {
		if got[i] != vec[i] {
			t.Fatalf("round-trip[%d] = %v, want %v", i, got[i], vec[i])
		}
	}
}

// TestSameBasenameProjectsCoexist is the F14 regression: two document folders
// that share a basename (e.g. two ".../backend") must both index, keyed by their
// distinct identities — the dropped UNIQUE(name) no longer collides them.
func TestSameBasenameProjectsCoexist(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id1, err := s.EnsureProjectIdentity(ctx, "path:/one/backend", "backend", "/one/backend", "bge-m3", "docs", 0)
	if err != nil {
		t.Fatalf("first backend: %v", err)
	}
	id2, err := s.EnsureProjectIdentity(ctx, "path:/two/backend", "backend", "/two/backend", "bge-m3", "docs", 0)
	if err != nil {
		t.Fatalf("second backend (same name) must not collide: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("same-basename projects share id %d; want distinct", id1)
	}
	// Each resolves by its unique identity.
	p1, err := s.GetProjectByIdentity(ctx, "path:/one/backend")
	if err != nil || p1.ID != id1 || p1.Path != "/one/backend" {
		t.Errorf("GetProjectByIdentity(one) = %+v, %v", p1, err)
	}
	p2, err := s.GetProjectByIdentity(ctx, "path:/two/backend")
	if err != nil || p2.ID != id2 || p2.Path != "/two/backend" {
		t.Errorf("GetProjectByIdentity(two) = %+v, %v", p2, err)
	}
}
