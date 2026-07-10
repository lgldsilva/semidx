package localstore

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestPruneOrphanEmbeddings verifies ADR-7 RF05: a dictionary vector is kept
// while any chunk references it, and removed once the referencing chunks are
// gone (here, by deleting the project).
func TestPruneOrphanEmbeddings(t *testing.T) {
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
	if err := s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "x"}}, [][]float32{{1, 0, 0}}, 3); err != nil {
		t.Fatal(err)
	}

	// Still referenced -> GC removes nothing.
	if n, err := s.PruneOrphanEmbeddings(ctx); err != nil || n != 0 {
		t.Fatalf("GC while referenced = %d err=%v, want 0", n, err)
	}

	if err := s.DeleteProject(ctx, "p"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	// Now orphaned -> GC removes the one vector.
	n, err := s.PruneOrphanEmbeddings(ctx)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if n != 1 {
		t.Fatalf("GC removed %d, want 1", n)
	}
	var left int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM unique_embeddings`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("unique_embeddings left = %d, want 0", left)
	}
}
