// Package indexstoretest provides shared contract tests for store.IndexStore
// implementations (PostgreSQL and SQLite).
package indexstoretest

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// Run exercises the IndexStore contract. The factory must return a fresh store.
func Run(t *testing.T, factory func(t *testing.T) store.IndexStore) {
	t.Helper()
	ctx := context.Background()
	s := factory(t)

	const dims = 3
	const model = "test-3d"
	id, err := s.UpsertProject(ctx, "conformance", "/tmp/conformance", model, dims)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	p, err := s.GetProject(ctx, "conformance")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.ID != id || p.Name != "conformance" {
		t.Fatalf("GetProject = %+v, want id=%d name=conformance", p, id)
	}

	list, err := s.ListProjects(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 1 || list[0].Name != "conformance" {
		t.Fatalf("ListProjects = %+v", list)
	}

	fileID, err := s.UpsertFile(ctx, id, "main.go", "abc123", 12)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if ens, ok := s.(interface {
		EnsureChunksTable(context.Context, int) error
	}); ok {
		if err := ens.EnsureChunksTable(ctx, dims); err != nil {
			t.Fatalf("EnsureChunksTable: %v", err)
		}
	}
	chunks := []chunker.Chunk{{Content: "package main", StartLine: 1, EndLine: 1}}
	emb := [][]float32{{0.1, 0.2, 0.3}}
	if err := s.InsertChunks(ctx, id, fileID, chunks, emb, dims); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	results, err := s.SearchSimilarKeywords(ctx, id, "package main func", dims, 5)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SearchSimilarKeywords returned no hits")
	}
	if results[0].FilePath != "main.go" {
		t.Fatalf("hit path = %q, want main.go", results[0].FilePath)
	}

	runGraphChunkBatch(ctx, t, s, id, dims)
}

// runGraphChunkBatch exercises the optional GraphChunkBatchStore extension that
// graph expansion uses to collect every discovered file's chunks in one round
// trip. Stores that do not implement it are skipped.
func runGraphChunkBatch(ctx context.Context, t *testing.T, s store.IndexStore, projectID, dims int) {
	t.Helper()
	batch, ok := s.(store.GraphChunkBatchStore)
	if !ok {
		return
	}

	// A second file with several chunks, so the per-file limit is observable.
	multiID, err := s.UpsertFile(ctx, projectID, "pkg/multi.go", "hash-multi", 30)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := s.InsertChunks(ctx, projectID, multiID, []chunker.Chunk{
		{Content: "chunk one", StartLine: 1, EndLine: 2},
		{Content: "chunk two", StartLine: 3, EndLine: 4},
		{Content: "chunk three", StartLine: 5, EndLine: 6},
	}, [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}, dims); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	got, err := batch.FetchChunksByPaths(ctx, projectID,
		[]string{"pkg/multi.go", "main.go", "pkg/missing.go"}, dims, 2)
	if err != nil {
		t.Fatalf("FetchChunksByPaths: %v", err)
	}
	// The limit applies per file, so one hot file cannot crowd out the rest.
	if len(got["pkg/multi.go"]) != 2 {
		t.Errorf("pkg/multi.go = %d chunks, want 2 (per-file limit)", len(got["pkg/multi.go"]))
	}
	if len(got["main.go"]) != 1 {
		t.Errorf("main.go = %d chunks, want 1", len(got["main.go"]))
	}
	if _, ok := got["pkg/missing.go"]; ok {
		t.Error("unknown paths must be absent from the result")
	}
	if got["pkg/multi.go"][0].Content != "chunk one" {
		t.Errorf("first chunk = %q, want the lowest chunk_index", got["pkg/multi.go"][0].Content)
	}

	if out, err := batch.FetchChunksByPaths(ctx, projectID, nil, dims, 2); err != nil || out != nil {
		t.Errorf("no paths = %+v, err=%v; want a nil no-op", out, err)
	}
	if out, err := batch.FetchChunksByPaths(ctx, projectID, []string{"main.go"}, dims, 0); err != nil || out != nil {
		t.Errorf("zero limit = %+v, err=%v; want a nil no-op", out, err)
	}
	// Paths are bound parameters: a LIKE metacharacter matches literally.
	if out, err := batch.FetchChunksByPaths(ctx, projectID, []string{"pkg/%"}, dims, 2); err != nil || len(out) != 0 {
		t.Errorf("wildcard path = %+v, err=%v; want no prefix matching", out, err)
	}
}
