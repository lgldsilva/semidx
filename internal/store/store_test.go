package store

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestChunksTableValidation is a pure unit test (no container) for the dynamic
// table-name guard.
func TestChunksTableValidation(t *testing.T) {
	for _, bad := range []int{0, -1, maxDims + 1} {
		if _, err := chunksTable(bad); err == nil {
			t.Errorf("chunksTable(%d) = nil error, want rejection", bad)
		}
	}
	got, err := chunksTable(1024)
	if err != nil {
		t.Fatalf("chunksTable(1024) errored: %v", err)
	}
	if got != `"chunks_1024"` {
		t.Errorf("chunksTable(1024) = %s, want quoted identifier \"chunks_1024\"", got)
	}
}

// newTestStore starts a throwaway pgvector container and applies the base
// schema (mirrors init.sql). Skips when no Docker provider is available.
func newTestStore(t *testing.T) *PgStore {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "pgvector/pgvector:pg16",
		postgres.WithDatabase("semantic_indexer"),
		postgres.WithUsername("semantic"),
		postgres.WithPassword("semantic"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start pgvector container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// NewPgStore applies the goose migrations, so the schema is ready here.
	s, err := NewPgStore(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPgStore: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestProjectLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.UpsertProject(ctx, "demo", "/tmp/demo", "bge-m3")
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	p, err := s.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.ID != id || p.Path != "/tmp/demo" || p.Model != "bge-m3" || p.Status != "indexing" {
		t.Errorf("GetProject = %+v, unexpected", p)
	}

	if err := s.UpdateProjectStatus(ctx, id, "ready"); err != nil {
		t.Fatalf("UpdateProjectStatus: %v", err)
	}
	p, _ = s.GetProject(ctx, "demo")
	if p.Status != "ready" {
		t.Errorf("status = %q, want ready", p.Status)
	}

	// Upsert is idempotent on name and resets status to indexing.
	id2, _ := s.UpsertProject(ctx, "demo", "/tmp/demo2", "bge-m3")
	if id2 != id {
		t.Errorf("re-upsert changed id: %d != %d", id2, id)
	}
}

func TestChunkRoundTripAndSearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, err := s.UpsertProject(ctx, "proj", "/tmp/proj", "test-3d")
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	fid, err := s.UpsertFile(ctx, pid, "src/auth.go", "hash1", 100)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	chunks := []chunker.Chunk{
		{Content: "alpha auth token handler", StartLine: 10, EndLine: 12},
		{Content: "beta gamma delta", StartLine: 20, EndLine: 20},
	}
	embeddings := [][]float32{{1, 0, 0}, {0, 1, 0}}
	if err := s.InsertChunks(ctx, pid, fid, chunks, embeddings, 3); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	// Vector search: query closest to the first chunk's embedding.
	res, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 5)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("SearchSimilar returned %d results, want 2", len(res))
	}
	if res[0].Content != "alpha auth token handler" {
		t.Errorf("top hit = %q, want the alpha chunk", res[0].Content)
	}
	if res[0].Score <= res[1].Score {
		t.Errorf("scores not descending: %v <= %v", res[0].Score, res[1].Score)
	}
	if res[0].FilePath != "src/auth.go" {
		t.Errorf("file path = %q, want src/auth.go", res[0].FilePath)
	}
	if res[0].StartLine != 10 || res[0].EndLine != 12 {
		t.Errorf("top hit line range = [%d,%d], want [10,12]", res[0].StartLine, res[0].EndLine)
	}

	// Keyword fallback: ILIKE on content.
	kw, err := s.SearchSimilarKeywords(ctx, pid, "auth", 3, 5)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords: %v", err)
	}
	if len(kw) != 1 || kw[0].Content != "alpha auth token handler" {
		t.Errorf("keyword search = %+v, want the alpha chunk", kw)
	}

	// Keyword search with unknown dims should probe and still find the chunk.
	kw2, err := s.SearchSimilarKeywords(ctx, pid, "gamma", 0, 5)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords(dims=0): %v", err)
	}
	if len(kw2) != 1 || kw2[0].Content != "beta gamma delta" {
		t.Errorf("probed keyword search = %+v, want the beta chunk", kw2)
	}

	// Re-inserting the same chunk indexes (upsert) rather than duplicating.
	if err := s.InsertChunks(ctx, pid, fid, chunks[:1], embeddings[:1], 3); err != nil {
		t.Fatalf("re-InsertChunks: %v", err)
	}
	again, _ := s.SearchSimilarKeywords(ctx, pid, "auth", 3, 5)
	if len(again) != 1 {
		t.Errorf("after re-insert got %d rows, want 1 (upsert, no dup)", len(again))
	}

	// DropAll clears everything.
	if err := s.DropAll(ctx); err != nil {
		t.Fatalf("DropAll: %v", err)
	}
	if _, err := s.GetProject(ctx, "proj"); err == nil {
		t.Error("GetProject after DropAll should fail (no rows)")
	}
}

func TestTextOnlyChunksKeywordOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, _ := s.UpsertProject(ctx, "p", "/p", "test-3d")

	// One embedded chunk and one text-only (embedding NULL) chunk.
	efid, _ := s.UpsertFile(ctx, pid, "code.go", "h1", 1)
	_ = s.InsertChunks(ctx, pid, efid, []chunker.Chunk{{Content: "embedded vector chunk"}}, [][]float32{{1, 0, 0}}, 3)

	tfid, _ := s.UpsertFile(ctx, pid, ".env", "h2", 1)
	if err := s.InsertChunksTextOnly(ctx, pid, tfid, []chunker.Chunk{{Content: "SECRET_TOKEN plaintext only"}}, 3); err != nil {
		t.Fatalf("InsertChunksTextOnly: %v", err)
	}

	// Vector search must NOT return the text-only (NULL embedding) row.
	vec, err := s.SearchSimilar(ctx, pid, []float32{0, 0, 1}, 3, 10)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	for _, r := range vec {
		if r.FilePath == ".env" {
			t.Error("SECURITY: text-only chunk leaked into vector search results")
		}
	}

	// Keyword search MUST find the text-only content.
	kw, err := s.SearchSimilarKeywords(ctx, pid, "SECRET_TOKEN", 3, 10)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords: %v", err)
	}
	if len(kw) != 1 || kw[0].FilePath != ".env" {
		t.Errorf("keyword search = %+v, want the text-only .env chunk", kw)
	}
}

func TestFileUpToDate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, _ := s.UpsertProject(ctx, "p", "/p", "test-3d")
	fid, _ := s.UpsertFile(ctx, pid, "x.go", "hash-v1", 10)

	// No chunks yet → not up to date even though the hash matches.
	if up, err := s.FileUpToDate(ctx, pid, "x.go", "hash-v1", 3); err != nil || up {
		t.Errorf("FileUpToDate (no chunks) = %v, err %v; want false", up, err)
	}

	_ = s.InsertChunks(ctx, pid, fid, []chunker.Chunk{{Content: "code"}}, [][]float32{{1, 0, 0}}, 3)

	// Same hash + chunks present → up to date.
	if up, err := s.FileUpToDate(ctx, pid, "x.go", "hash-v1", 3); err != nil || !up {
		t.Errorf("FileUpToDate (hash match + chunks) = %v, err %v; want true", up, err)
	}
	// Changed hash → not up to date (needs reindex).
	if up, err := s.FileUpToDate(ctx, pid, "x.go", "hash-v2", 3); err != nil || up {
		t.Errorf("FileUpToDate (hash changed) = %v, err %v; want false", up, err)
	}
	// Unknown file → not up to date.
	if up, err := s.FileUpToDate(ctx, pid, "other.go", "hash-v1", 3); err != nil || up {
		t.Errorf("FileUpToDate (unknown file) = %v, err %v; want false", up, err)
	}
}

func TestDeleteChunksForFile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, _ := s.UpsertProject(ctx, "p", "/p", "test-3d")
	fid, _ := s.UpsertFile(ctx, pid, "f.go", "h", 1)
	_ = s.InsertChunks(ctx, pid, fid, []chunker.Chunk{{Content: "keepme"}}, [][]float32{{1, 0, 0}}, 3)

	if err := s.DeleteChunksForFile(ctx, pid, fid, 3); err != nil {
		t.Fatalf("DeleteChunksForFile: %v", err)
	}
	res, _ := s.SearchSimilarKeywords(ctx, pid, "keepme", 3, 5)
	if len(res) != 0 {
		t.Errorf("after delete got %d rows, want 0", len(res))
	}
}
