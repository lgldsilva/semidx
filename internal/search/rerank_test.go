package search

import (
	"context"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestLexicalReranker_LiftsTermMatchesOnTiedScores(t *testing.T) {
	// All results share the same score (as in keyword mode), so ordering must
	// come purely from query-term overlap.
	in := []store.SearchResult{
		{FilePath: "a.go", Content: "unrelated helper code", Score: 0.5},
		{FilePath: "b.go", Content: "token refresh and auth handling", Score: 0.5},
		{FilePath: "c.go", Content: "auth only", Score: 0.5},
	}
	out := NewLexicalReranker(0.5).Rerank(context.Background(), "auth token", in)
	if out[0].FilePath != "b.go" {
		t.Fatalf("expected b.go first (both terms), got %s", out[0].FilePath)
	}
	if out[len(out)-1].FilePath != "a.go" {
		t.Fatalf("expected a.go last (no terms), got %s", out[len(out)-1].FilePath)
	}
}

func TestLexicalReranker_PreservesScoreField(t *testing.T) {
	in := []store.SearchResult{
		{FilePath: "a.go", Content: "auth token", Score: 0.42},
		{FilePath: "b.go", Content: "nothing", Score: 0.91},
	}
	out := NewLexicalReranker(0.5).Rerank(context.Background(), "auth", in)
	// The original similarity score must be preserved for display.
	for _, r := range out {
		if r.FilePath == "a.go" && r.Score != 0.42 {
			t.Errorf("score mutated: %v", r.Score)
		}
	}
}

func TestLexicalReranker_NoOpCases(t *testing.T) {
	rr := NewLexicalReranker(0.5)
	single := []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 1}}
	if got := rr.Rerank(context.Background(), "q", single); len(got) != 1 || got[0].FilePath != "a.go" {
		t.Error("single result must be returned unchanged")
	}
	two := []store.SearchResult{{FilePath: "a.go", Score: 0.9}, {FilePath: "b.go", Score: 0.1}}
	// Empty/too-short query -> no tokens -> input order preserved.
	if got := rr.Rerank(context.Background(), "  a ", two); got[0].FilePath != "a.go" {
		t.Error("empty-token query must preserve input order")
	}
}

func TestNewLexicalReranker_ClampsWeight(t *testing.T) {
	if NewLexicalReranker(-1).Weight != 0 {
		t.Error("negative weight must clamp to 0")
	}
	if NewLexicalReranker(5).Weight != 1 {
		t.Error("weight > 1 must clamp to 1")
	}
}

// TestSearchAppliesReranker checks the Service wiring: with a reranker set, the
// top-K is reordered by term overlap; without one, the store order is preserved.
func TestSearchAppliesReranker(t *testing.T) {
	newStore := func() *fakeStore {
		return &fakeStore{
			project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
			kwResults: []store.SearchResult{
				{FilePath: "a.go", Content: "unrelated helper", Score: 0.5},
				{FilePath: "b.go", Content: "auth token handling", Score: 0.5},
			},
		}
	}
	req := Request{Project: "p", Query: "auth token", TopK: 5, KeywordOnly: true}

	// Without a reranker: store order preserved (a.go first).
	base := NewService(newStore(), &fakeEmbedder{})
	r1, err := base.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Results[0].FilePath != "a.go" {
		t.Fatalf("baseline order changed unexpectedly: %s first", r1.Results[0].FilePath)
	}

	// With the reranker: b.go (both query terms) is lifted to the top.
	ranked := NewService(newStore(), &fakeEmbedder{})
	ranked.SetReranker(NewLexicalReranker(0.6))
	r2, err := ranked.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Results[0].FilePath != "b.go" {
		t.Fatalf("reranker did not lift the term-matching result: %s first", r2.Results[0].FilePath)
	}
}

func TestTokenize(t *testing.T) {
	set := tokenize("Auth-Token, refresh() a")
	for _, want := range []string{"auth", "token", "refresh"} {
		if _, ok := set[want]; !ok {
			t.Errorf("missing token %q in %v", want, set)
		}
	}
	if _, ok := set["a"]; ok {
		t.Error("single-char token should be dropped")
	}
}
