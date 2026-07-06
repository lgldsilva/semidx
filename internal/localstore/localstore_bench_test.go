package localstore

import (
	"context"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// benchStore opens a SQLiteStore backed by a fresh temp-file DB, like
// newTestStore but accepting a *testing.B.
func benchStore(b *testing.B) *SQLiteStore {
	b.Helper()
	path := filepath.Join(b.TempDir(), "index.db")
	s, err := New(path)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	b.Cleanup(s.Close)
	return s
}

// makeFloats generates a dims-length random float32 slice.
func makeFloats(dims int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = rand.Float32()
	}
	return v
}

// ---------------------------------------------------------------------------
// Low-level operations
// ---------------------------------------------------------------------------

func BenchmarkCosineBruteForce_768d(b *testing.B) {
	b.ReportAllocs()
	dims := 768
	query := make([]float32, dims)
	candidate := make([]float32, dims)
	for i := range query {
		query[i] = rand.Float32()
		candidate[i] = rand.Float32()
	}
	b.ResetTimer()
	for b.Loop() {
		cosineSimilarity(query, candidate)
	}
}

func BenchmarkCosineBruteForce_1024d(b *testing.B) {
	b.ReportAllocs()
	dims := 1024
	query := make([]float32, dims)
	candidate := make([]float32, dims)
	for i := range query {
		query[i] = rand.Float32()
		candidate[i] = rand.Float32()
	}
	b.ResetTimer()
	for b.Loop() {
		cosineSimilarity(query, candidate)
	}
}

func BenchmarkDecodeEmbedding_768d(b *testing.B) {
	b.ReportAllocs()
	dims := 768
	floats := make([]float32, dims)
	for i := range floats {
		floats[i] = rand.Float32()
	}
	blob := encodeEmbedding(floats)
	b.ResetTimer()
	for b.Loop() {
		decodeEmbedding(blob)
	}
}

// ---------------------------------------------------------------------------
// InsertChunks
// ---------------------------------------------------------------------------

// BenchmarkInsertChunks_10 measures InsertChunks with 10 chunks (a small file).
func BenchmarkInsertChunks_10(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	b.StopTimer()
	s := benchStore(b)
	projectID, err := s.UpsertProject(ctx, "bench", "/tmp/bench", "bge-m3", 768)
	if err != nil {
		b.Fatalf("UpsertProject: %v", err)
	}
	fileID, err := s.UpsertFile(ctx, projectID, "bench.go", "hash1", 500)
	if err != nil {
		b.Fatalf("UpsertFile: %v", err)
	}
	chunks := make([]chunker.Chunk, 10)
	embeddings := make([][]float32, 10)
	for i := range chunks {
		chunks[i] = chunker.Chunk{
			Content:   "func benchFunc() string { return \"hello world\" }",
			StartLine: i*3 + 1,
			EndLine:   i*3 + 3,
		}
		embeddings[i] = makeFloats(768)
	}
	b.StartTimer()

	for b.Loop() {
		_ = s.InsertChunks(ctx, projectID, fileID, chunks, embeddings, 768)
	}
}

// BenchmarkInsertChunks_100 measures InsertChunks with 100 chunks (a medium
// file), stressing the SQLite batch insert.
func BenchmarkInsertChunks_100(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	b.StopTimer()
	s := benchStore(b)
	projectID, err := s.UpsertProject(ctx, "bench", "/tmp/bench", "bge-m3", 768)
	if err != nil {
		b.Fatalf("UpsertProject: %v", err)
	}
	fileID, err := s.UpsertFile(ctx, projectID, "bench.go", "hash1", 5000)
	if err != nil {
		b.Fatalf("UpsertFile: %v", err)
	}
	chunks := make([]chunker.Chunk, 100)
	embeddings := make([][]float32, 100)
	for i := range chunks {
		chunks[i] = chunker.Chunk{
			Content:   "func benchFunc() string { return \"hello world\" }",
			StartLine: i*3 + 1,
			EndLine:   i*3 + 3,
		}
		embeddings[i] = makeFloats(768)
	}
	b.StartTimer()

	for b.Loop() {
		_ = s.InsertChunks(ctx, projectID, fileID, chunks, embeddings, 768)
	}
}

// ---------------------------------------------------------------------------
// SearchSimilar
// ---------------------------------------------------------------------------

// BenchmarkSearchSimilar_1K benchmarks SearchSimilar against an index with
// ~1000 stored chunks so the brute-force cosine scan has real work to do.
func BenchmarkSearchSimilar_1K(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()

	b.StopTimer()
	s := benchStore(b)
	projectID, err := s.UpsertProject(ctx, "bench", "/tmp/bench", "bge-m3", 768)
	if err != nil {
		b.Fatalf("UpsertProject: %v", err)
	}
	fileID, err := s.UpsertFile(ctx, projectID, "bench.go", "hash1", 50000)
	if err != nil {
		b.Fatalf("UpsertFile: %v", err)
	}
	const numChunks = 1000
	chunks := make([]chunker.Chunk, numChunks)
	embeddings := make([][]float32, numChunks)
	for i := range chunks {
		chunks[i] = chunker.Chunk{
			Content:   "func benchFunc() string { return \"hello world\" }",
			StartLine: i*3 + 1,
			EndLine:   i*3 + 3,
		}
		embeddings[i] = makeFloats(768)
	}
	if err := s.InsertChunks(ctx, projectID, fileID, chunks, embeddings, 768); err != nil {
		b.Fatalf("InsertChunks: %v", err)
	}

	query := makeFloats(768)
	b.StartTimer()

	for b.Loop() {
		_, _ = s.SearchSimilar(ctx, projectID, query, 768, 20)
	}
}
