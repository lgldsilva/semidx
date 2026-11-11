package store

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/tenant"
)

// otherTenantCtx creates a second tenant and returns a context pinned to it.
func otherTenantCtx(t *testing.T, s *PgStore) context.Context {
	t.Helper()
	other, err := s.CreateTenant(context.Background(), uniqueSlug(t, "other"), "Other")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return tenant.MustWith(context.Background(), tenant.Context{ID: other.ID})
}

// Regression: DropAll used TRUNCATE ... CASCADE plus DROP of every chunks_*
// table, wiping every tenant's projects and chunks. It must only remove the
// calling tenant's data.
func TestDropAllKeepsOtherTenants(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	otherCtx := otherTenantCtx(t, s)

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	mine, err := s.UpsertProject(ctx, "mine", "/mine", "m", 3)
	if err != nil {
		t.Fatalf("UpsertProject mine: %v", err)
	}
	theirs, err := s.UpsertProject(otherCtx, "theirs", "/theirs", "m", 3)
	if err != nil {
		t.Fatalf("UpsertProject theirs: %v", err)
	}
	fid, err := s.UpsertFile(otherCtx, theirs, "a.go", "h1", 1)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := s.InsertChunks(otherCtx, theirs, fid, []chunker.Chunk{{Content: "survivor", StartLine: 1, EndLine: 1}},
		[][]float32{{1, 0, 0}}, 3); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	if err := s.DropAll(ctx); err != nil {
		t.Fatalf("DropAll: %v", err)
	}
	if _, err := s.GetProjectByID(ctx, mine); err == nil {
		t.Error("caller tenant project survived DropAll")
	}
	if _, err := s.GetProjectByID(otherCtx, theirs); err != nil {
		t.Fatalf("other tenant project was dropped: %v", err)
	}
	got, err := s.FetchChunksByPath(otherCtx, theirs, "a.go", 3, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("other tenant chunks = %d, err=%v; want 1", len(got), err)
	}
}
