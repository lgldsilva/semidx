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
}
