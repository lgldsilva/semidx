package localstore

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestInsertChunksDeduplicatesEmbeddings verifies ADR-7 storage de-dup: the same
// content indexed in two projects embeds to the same vector, which is stored
// once in unique_embeddings while each project keeps its own chunk row, and
// search still resolves the vector via the dictionary.
func TestInsertChunksDeduplicatesEmbeddings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	emb := []float32{0.1, 0.2, 0.3}
	chunk := chunker.Chunk{Content: "shared code", StartLine: 1, EndLine: 1}

	for _, name := range []string{"proj-a", "proj-b"} {
		pid, err := s.UpsertProject(ctx, name, "/tmp/"+name, "bge-m3", 3)
		if err != nil {
			t.Fatalf("UpsertProject %s: %v", name, err)
		}
		fid, err := s.UpsertFile(ctx, pid, "shared.go", "h1", 10)
		if err != nil {
			t.Fatalf("UpsertFile %s: %v", name, err)
		}
		if err := s.InsertChunks(ctx, pid, fid, []chunker.Chunk{chunk}, [][]float32{emb}, 3); err != nil {
			t.Fatalf("InsertChunks %s: %v", name, err)
		}
		res, err := s.SearchSimilar(ctx, pid, emb, 3, 5)
		if err != nil || len(res) != 1 {
			t.Fatalf("search in %s: %d results err=%v", name, len(res), err)
		}
		if res[0].Content != "shared code" {
			t.Fatalf("search content in %s = %q", name, res[0].Content)
		}
	}

	var dictRows, chunkRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM unique_embeddings`).Scan(&dictRows); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&chunkRows); err != nil {
		t.Fatal(err)
	}
	if chunkRows != 2 {
		t.Fatalf("chunk rows = %d, want 2", chunkRows)
	}
	if dictRows != 1 {
		t.Fatalf("unique_embeddings rows = %d, want 1 (dedup failed)", dictRows)
	}
}

// TestInsertChunksDistinctVectorsNotDeduped guards the dedup key: different
// vectors must produce distinct dictionary rows.
func TestInsertChunksDistinctVectorsNotDeduped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pid, err := s.UpsertProject(ctx, "p", "/tmp/p", "bge-m3", 3)
	if err != nil {
		t.Fatal(err)
	}
	fid, err := s.UpsertFile(ctx, pid, "f.go", "h", 10)
	if err != nil {
		t.Fatal(err)
	}
	chunks := []chunker.Chunk{{Content: "a"}, {Content: "b"}}
	embs := [][]float32{{1, 0, 0}, {0, 1, 0}}
	if err := s.InsertChunks(ctx, pid, fid, chunks, embs, 3); err != nil {
		t.Fatal(err)
	}

	var dictRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM unique_embeddings`).Scan(&dictRows); err != nil {
		t.Fatal(err)
	}
	if dictRows != 2 {
		t.Fatalf("unique_embeddings rows = %d, want 2", dictRows)
	}
}
