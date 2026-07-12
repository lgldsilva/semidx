package search

import (
	"context"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// TestSearchMulti_provenanceInEnvelope is the regression test for the fix that
// stopped the internal "identity\x00path" scope label from leaking into the
// caller's FilePath (it corrupted MCP/JSON output). Provenance must now come
// back in the explicit Project field with a clean FilePath.
func TestSearchMulti_provenanceInEnvelope(t *testing.T) {
	st := &fakeStore{
		project:    &store.Project{ID: 1, Name: "app", Identity: "proj-app", Model: "bge-m3"},
		simResults: []store.SearchResult{{FilePath: "cmd/main.go", Content: "x", Score: 0.9}},
	}
	emb := &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	resp, err := svc.SearchMulti(context.Background(), MultiScopeRequest{
		Identities: []string{"proj-app"},
		Query:      "main",
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("SearchMulti: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 result, got %d: %+v", len(resp.Results), resp.Results)
	}

	r := resp.Results[0]
	if strings.ContainsRune(r.FilePath, 0) {
		t.Errorf("FilePath must not contain the \\x00 provenance separator: %q", r.FilePath)
	}
	if r.FilePath != "cmd/main.go" {
		t.Errorf("FilePath = %q, want clean %q", r.FilePath, "cmd/main.go")
	}
	if r.Project != "proj-app" {
		t.Errorf("Project = %q, want %q", r.Project, "proj-app")
	}
}

// TestSearchMulti_noIdentities guards the early error path.
func TestSearchMulti_noIdentities(t *testing.T) {
	svc := NewService(&fakeStore{}, &fakeEmbedder{})
	if _, err := svc.SearchMulti(context.Background(), MultiScopeRequest{Query: "x"}); err == nil {
		t.Error("SearchMulti with no identities should error")
	}
}
