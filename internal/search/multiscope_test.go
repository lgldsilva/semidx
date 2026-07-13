package search

import (
	"context"
	"errors"
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

// TestSearchMulti_aggregatesKeywordFlag is the audit regression (MÉDIA #4):
// SearchMulti must propagate the sub-searches' Keyword/Fallback flags instead of
// zeroing them, so a client isn't handed keyword ranking labeled as semantic.
func TestSearchMulti_aggregatesKeywordFlag(t *testing.T) {
	st := &fakeStore{
		project:    &store.Project{ID: 1, Name: "app", Identity: "proj-app", Model: "bge-m3"},
		simResults: []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.9}},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3})
	resp, err := svc.SearchMulti(context.Background(), MultiScopeRequest{
		Identities:  []string{"proj-app"},
		Query:       "main",
		TopK:        5,
		KeywordOnly: true, // forces the keyword path → resp.Keyword on each sub-search
	})
	if err != nil {
		t.Fatalf("SearchMulti: %v", err)
	}
	if !resp.Keyword {
		t.Error("MultiResponse.Keyword must aggregate the sub-search keyword flag")
	}
}

// TestSearchAllProjects_tagsAndFuses verifies the global-chat search lists
// projects, tags each hit with its project, and returns a clean FilePath.
func TestSearchAllProjects_tagsAndFuses(t *testing.T) {
	st := &fakeStore{
		project:    &store.Project{ID: 1, Name: "app", Identity: "proj-app", Model: "bge-m3"},
		simResults: []store.SearchResult{{FilePath: "cmd/main.go", Content: "x", Score: 0.9}},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3})
	resp, err := svc.SearchAllProjects(context.Background(), MultiScopeRequest{Query: "main", TopK: 5})
	if err != nil {
		t.Fatalf("SearchAllProjects: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("want 1 result, got %d: %+v", len(resp.Results), resp.Results)
	}
	r := resp.Results[0]
	if strings.ContainsRune(r.FilePath, 0) {
		t.Errorf("FilePath must not contain the \\x00 separator: %q", r.FilePath)
	}
	if r.FilePath != "cmd/main.go" || r.Project != "proj-app" {
		t.Errorf("result = %+v, want clean path tagged proj-app", r)
	}
}

// TestSearchAllProjects_empty returns an empty (non-nil) response when no
// projects are indexed, not an error.
func TestSearchAllProjects_empty(t *testing.T) {
	svc := NewService(&fakeStore{}, &fakeEmbedder{})
	resp, err := svc.SearchAllProjects(context.Background(), MultiScopeRequest{Query: "x"})
	if err != nil {
		t.Fatalf("SearchAllProjects: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("want no results, got %+v", resp.Results)
	}
}

// TestSearchAllProjects_listError surfaces a project-listing failure.
func TestSearchAllProjects_listError(t *testing.T) {
	svc := NewService(&fakeStore{listErr: errors.New("boom")}, &fakeEmbedder{})
	if _, err := svc.SearchAllProjects(context.Background(), MultiScopeRequest{Query: "x"}); err == nil {
		t.Error("expected a list-projects error")
	}
}
