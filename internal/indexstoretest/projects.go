package indexstoretest

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// runProjectNameAmbiguity covers by-name lookups now that the project name is
// not unique: an exact identity match wins, and a name shared by several
// projects is reported as ambiguous instead of picking (or deleting) them all.
func runProjectNameAmbiguity(ctx context.Context, t *testing.T, s store.IndexStore, dims int) {
	t.Helper()
	first, err := s.EnsureProjectIdentity(ctx, "path:/a/dup", "dup", "/a/dup", "m", "local", dims)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity a: %v", err)
	}
	second, err := s.EnsureProjectIdentity(ctx, "path:/b/dup", "dup", "/b/dup", "m", "local", dims)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity b: %v", err)
	}
	if _, err := s.GetProject(ctx, "dup"); !errors.Is(err, store.ErrProjectAmbiguous) {
		t.Errorf("GetProject(shared name) err = %v, want ErrProjectAmbiguous", err)
	}
	if err := s.DeleteProject(ctx, "dup"); !errors.Is(err, store.ErrProjectAmbiguous) {
		t.Errorf("DeleteProject(shared name) err = %v, want ErrProjectAmbiguous", err)
	}
	for _, id := range []int{first, second} {
		if _, err := s.GetProjectByID(ctx, id); err != nil {
			t.Errorf("project %d deleted by an ambiguous name: %v", id, err)
		}
	}

	// A project whose identity equals the requested name is an exact match.
	exact, err := s.UpsertProject(ctx, "dup", "/srv/dup", "m", dims)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if p, err := s.GetProject(ctx, "dup"); err != nil || p.ID != exact {
		t.Errorf("GetProject(identity match) = %+v, %v; want id %d", p, err, exact)
	}
	if err := s.DeleteProject(ctx, "dup"); err != nil {
		t.Fatalf("DeleteProject(identity match): %v", err)
	}
	if _, err := s.GetProjectByID(ctx, exact); err == nil {
		t.Error("identity-matched project survived DeleteProject")
	}
	for _, id := range []int{first, second} {
		if _, err := s.GetProjectByID(ctx, id); err != nil {
			t.Errorf("unrelated same-name project %d deleted: %v", id, err)
		}
	}
}
