package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/embed"
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
		Query:      "find main",
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
	resp, err := svc.SearchAllProjects(context.Background(), MultiScopeRequest{Query: "find main", TopK: 5})
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

// TestSearchMulti_aggregatesDegraded: a degraded sub-search (embed circuit
// open) must mark the fused response degraded and carry the retry hint.
func TestSearchMulti_aggregatesDegraded(t *testing.T) {
	st := &fakeStore{
		project:   &store.Project{ID: 1, Name: "app", Identity: "proj-app", Model: "bge-m3"},
		kwResults: []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.5}},
	}
	emb := &fakeEmbedder{embedErr: &embed.RetryableError{Err: errors.New("circuit open"), After: 3 * time.Second}, dims: 3}
	svc := NewService(st, emb)

	resp, err := svc.SearchMulti(context.Background(), MultiScopeRequest{
		Identities: []string{"proj-app"},
		Query:      "find the main handler",
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("SearchMulti: %v", err)
	}
	if !resp.Degraded || resp.RetryAfter != 3*time.Second {
		t.Errorf("degraded=%v retryAfter=%v, want true/3s", resp.Degraded, resp.RetryAfter)
	}
	if !resp.Fallback || !resp.Keyword {
		t.Errorf("Degraded must imply Fallback and Keyword, got %v/%v", resp.Fallback, resp.Keyword)
	}
	if len(resp.Results) != 1 {
		t.Errorf("degraded search should still carry keyword results, got %+v", resp.Results)
	}
}

// TestSearchAllProjects_aggregatesDegraded mirrors the SearchMulti test for the
// global (cross-project) search path.
func TestSearchAllProjects_aggregatesDegraded(t *testing.T) {
	st := &fakeStore{
		project:   &store.Project{ID: 1, Name: "app", Identity: "proj-app", Model: "bge-m3"},
		kwResults: []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.5}},
	}
	emb := &fakeEmbedder{embedErr: &embed.RetryableError{Err: errors.New("circuit open"), After: time.Second}, dims: 3}
	svc := NewService(st, emb)

	resp, err := svc.SearchAllProjects(context.Background(), MultiScopeRequest{Query: "find the main handler", TopK: 5})
	if err != nil {
		t.Fatalf("SearchAllProjects: %v", err)
	}
	if !resp.Degraded || resp.RetryAfter != time.Second {
		t.Errorf("degraded=%v retryAfter=%v, want true/1s", resp.Degraded, resp.RetryAfter)
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

func TestFuseRankedResultsUsesReciprocalRankAcrossProjects(t *testing.T) {
	resp := fuseRankedResults([]rankedResult{
		{result: store.SearchResult{FilePath: "alpha\x00deep.go", Score: 0.99}, project: "alpha", sourceRank: 2},
		{result: store.SearchResult{FilePath: "beta\x00main.go", Score: 0.60}, project: "beta", sourceRank: 1},
	}, 0, 0, 5, aggFlags{})
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	if resp.Results[0].Project != "beta" {
		t.Fatalf("RRF order = %q then %q, want beta first", resp.Results[0].Project, resp.Results[1].Project)
	}
	if resp.Results[0].FusionScore <= resp.Results[1].FusionScore {
		t.Fatalf("fusion scores = %v, %v; want descending", resp.Results[0].FusionScore, resp.Results[1].FusionScore)
	}
	if resp.Results[0].Score != 0.60 {
		t.Fatalf("original similarity score was changed: %v", resp.Results[0].Score)
	}
}

func TestFuseRankedResultsAggregatesDuplicateCandidate(t *testing.T) {
	resp := fuseRankedResults([]rankedResult{
		{result: store.SearchResult{FilePath: "p\x00same.go", StartLine: 1, EndLine: 2, Score: 0.4}, project: "p", sourceRank: 3},
		{result: store.SearchResult{FilePath: "p\x00same.go", StartLine: 1, EndLine: 2, Score: 0.8}, project: "p", sourceRank: 1},
	}, 0, 0, 5, aggFlags{})
	if len(resp.Results) != 1 {
		t.Fatalf("duplicate candidate count = %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Score != 0.8 || resp.Results[0].FusionScore <= 1/61.0 {
		t.Fatalf("duplicate fusion = %+v, want max original score and summed ranks", resp.Results[0])
	}
}
