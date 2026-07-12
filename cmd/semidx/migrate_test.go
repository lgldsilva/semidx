package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/localstore"
)

// TestMigrateRows exercises ExportChunks + migrateRows by copying between two
// SQLite stores (both satisfy store.IndexStore, so no Postgres is needed): the
// migrated chunks must be searchable in the target via the copied embeddings,
// and text-only chunks must stay text-only.
func TestMigrateRows(t *testing.T) {
	ctx := context.Background()

	src, err := localstore.New(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(src.Close)
	if err := src.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatal(err)
	}
	pid, _ := src.UpsertProject(ctx, "proj", "/proj", "m", 0)
	fid, _ := src.UpsertFile(ctx, pid, "alpha.go", "h1", 10)
	if err := src.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "alpha token", StartLine: 1, EndLine: 1}},
		[][]float32{{1, 0, 0}}, 3); err != nil {
		t.Fatal(err)
	}
	// A text-only (sensitive) file: NULL embedding must round-trip as text-only.
	sfid, _ := src.UpsertFile(ctx, pid, ".env", "h2", 5)
	if err := src.InsertChunksTextOnly(ctx, pid, sfid,
		[]chunker.Chunk{{Content: "SECRET=x", StartLine: 1, EndLine: 1}}, 3); err != nil {
		t.Fatal(err)
	}

	rows, err := src.ExportChunks(ctx, pid)
	if err != nil {
		t.Fatalf("ExportChunks: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("exported %d chunks, want 2", len(rows))
	}

	tgt, err := localstore.New(filepath.Join(t.TempDir(), "tgt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tgt.Close)
	tpid, _ := tgt.UpsertProject(ctx, "proj", "/proj", "m", 0)

	files, chunks, err := migrateRows(ctx, tgt, tpid, rows)
	if err != nil {
		t.Fatalf("migrateRows: %v", err)
	}
	if files != 2 || chunks != 2 {
		t.Fatalf("migrated files=%d chunks=%d, want 2/2", files, chunks)
	}

	// The vector chunk is searchable via the migrated embedding (no re-embedding).
	res, err := tgt.SearchSimilar(ctx, tpid, []float32{1, 0, 0}, 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].FilePath != "alpha.go" {
		t.Fatalf("vector search after migrate = %+v, want alpha.go", res)
	}
	// The text-only chunk stayed out of vector search but is keyword-searchable.
	kw, err := tgt.SearchSimilarKeywords(ctx, tpid, "SECRET", 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(kw) != 1 || kw[0].FilePath != ".env" {
		t.Fatalf("keyword search for text-only after migrate = %+v", kw)
	}
}
