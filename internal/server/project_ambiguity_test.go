package server

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// A project name shared by several projects must not delete them all: the
// store reports ErrProjectAmbiguous and the API answers 409.
func TestDeleteProjectAmbiguousIsConflict(t *testing.T) {
	t.Parallel()
	srv := New(&fakeStore{token: &store.Token{Scopes: []string{"write"}}, deleteErr: store.ErrProjectAmbiguous}, fakeEmbedder{}, nil)
	if rec := do(t, srv, "DELETE", "/api/v1/projects/dup", "tok", ""); rec.Code != 409 {
		t.Fatalf("ambiguous delete = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
}
