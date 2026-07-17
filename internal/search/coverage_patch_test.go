// coverage-patch: 2026-07-17
package search

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestHybridSearch_endToEnd(t *testing.T) {
	st := &fakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "m"},
		simResults: []store.SearchResult{
			{FilePath: "a.go", Content: "va", Score: 0.9, StartLine: 1},
		},
		kwResults: []store.SearchResult{
			{FilePath: "b.go", Content: "kb", Score: 0.5, StartLine: 2},
		},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3})
	got, err := svc.HybridSearch(context.Background(), 1, "query", "m", 3, 5, "")
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(got) < 1 {
		t.Fatalf("want results, got %v", got)
	}
}

func TestHybridSearch_embedError(t *testing.T) {
	svc := NewService(&fakeStore{}, &fakeEmbedder{embedErr: errors.New("no emb"), dims: 3})
	_, err := svc.HybridSearch(context.Background(), 1, "q", "m", 3, 5, "")
	if err == nil {
		t.Fatal("expected embed error")
	}
}

func TestApplyDiversity(t *testing.T) {
	t.Parallel()
	// No caps → nil (caller falls back).
	if applyDiversity(nil, 0, 0, 5) != nil {
		t.Error("no caps should return nil")
	}

	results := []store.SearchResult{
		{FilePath: "projA\x00f1.go", Score: 0.9},
		{FilePath: "projA\x00f1.go", Score: 0.8},
		{FilePath: "projA\x00f2.go", Score: 0.7},
		{FilePath: "projB\x00g1.go", Score: 0.6},
		{FilePath: "projB\x00g1.go", Score: 0.5},
		{FilePath: "projB\x00g2.go", Score: 0.4},
	}

	// Cap per file only.
	got := applyDiversity(results, 1, 0, 10)
	if len(got) != 4 { // f1, f2, g1, g2
		t.Errorf("maxPerFile=1: got %d, want 4", len(got))
	}

	// Cap per project only.
	got = applyDiversity(results, 0, 2, 10)
	if len(got) != 4 {
		t.Errorf("maxPerProject=2: got %d, want 4", len(got))
	}

	// Both caps + topK stop.
	got = applyDiversity(results, 1, 2, 2)
	if len(got) != 2 {
		t.Errorf("topK=2: got %d", len(got))
	}
}

func TestFuseResults_withDiversity(t *testing.T) {
	t.Parallel()
	all := []store.SearchResult{
		{FilePath: "p1\x00a.go", Score: 0.9, Content: "a"},
		{FilePath: "p1\x00a.go", Score: 0.8, Content: "a2"},
		{FilePath: "p2\x00b.go", Score: 0.7, Content: "b"},
	}
	flags := aggFlags{fallback: true, keyword: true}
	resp := fuseResults(all, 1, 1, 5, flags)
	if !resp.Fallback || !resp.Keyword {
		t.Errorf("flags not preserved: %+v", resp)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("want 2 after diversity, got %d: %+v", len(resp.Results), resp.Results)
	}
	for _, r := range resp.Results {
		if strings.ContainsRune(r.FilePath, 0) {
			t.Errorf("provenance leaked: %q", r.FilePath)
		}
		if r.Project == "" {
			t.Errorf("project empty for %+v", r)
		}
	}

	// Empty input.
	empty := fuseResults(nil, 1, 1, 5, aggFlags{})
	if len(empty.Results) != 0 {
		t.Error("empty fuse should have no results")
	}

	// No diversity caps: applyDiversity returns nil → fallback slice/topK.
	resp2 := fuseResults(all, 0, 0, 1, aggFlags{})
	if len(resp2.Results) != 1 {
		t.Errorf("topK=1 without diversity: got %d", len(resp2.Results))
	}
}

func TestSplitProvenance(t *testing.T) {
	t.Parallel()
	p, f := splitProvenance("proj\x00path/file.go")
	if p != "proj" || f != "path/file.go" {
		t.Errorf("got %q, %q", p, f)
	}
	p, f = splitProvenance("no-sep.go")
	if p != "" || f != "no-sep.go" {
		t.Errorf("no sep: %q, %q", p, f)
	}
}

func TestFormatResult_graphMatch(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	f := HumanFormatter{}
	r := store.SearchResult{FilePath: "dep.go", Content: "", Score: 0.5, StartLine: 1, EndLine: 1}
	if err := f.formatResult(&buf, 0, r, false, 100, 4); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(graph match)") {
		t.Errorf("want graph match label: %s", buf.String())
	}
}

func TestSearchMulti_skipsFailedProjectAndDefaultTopK(t *testing.T) {
	// One identity resolves, one does not — best-effort skip.
	st := &fakeStore{
		project:    &store.Project{ID: 1, Name: "app", Identity: "good", Model: "m"},
		simResults: []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.9}},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3})
	resp, err := svc.SearchMulti(context.Background(), MultiScopeRequest{
		Identities: []string{"good", "missing-id"},
		Query:      "search the app",
		// TopK 0 → default 5
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Error("expected results from good identity")
	}
}

func TestSearchAllProjects_defaultTopKAndNameLabel(t *testing.T) {
	// Document project without identity uses name as label.
	st := &fakeStore{
		project:    &store.Project{ID: 2, Name: "docs", Identity: "", Model: "m"},
		simResults: []store.SearchResult{{FilePath: "readme.md", Content: "x", Score: 0.8}},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3})
	resp, err := svc.SearchAllProjects(context.Background(), MultiScopeRequest{Query: "find the docs"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("got %d results", len(resp.Results))
	}
	if resp.Results[0].Project != "docs" {
		t.Errorf("project label = %q, want docs", resp.Results[0].Project)
	}
}

func TestFetchGraphChunks_errorAndEmpty(t *testing.T) {
	// Store that errors / returns empty for FetchChunksByDirPrefix.
	st := &chunkFetchStore{
		fakeStore: fakeStore{project: &store.Project{ID: 1, Name: "p", Model: "m"}},
		mode:      "err",
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1}, dims: 1})
	expanded := map[string]float64{"dep.go": 0.5}
	got := fetchGraphChunks(context.Background(), svc, 1, 1, expanded)
	if len(got) != 1 || got[0].FilePath != "dep.go" || got[0].Content != "" {
		t.Errorf("error path placeholder: %+v", got)
	}

	st.mode = "empty"
	got = fetchGraphChunks(context.Background(), svc, 1, 1, expanded)
	if len(got) != 1 || got[0].Content != "" {
		t.Errorf("empty path placeholder: %+v", got)
	}

	st.mode = "ok"
	got = fetchGraphChunks(context.Background(), svc, 1, 1, expanded)
	if len(got) != 1 || got[0].Content != "chunk" {
		t.Errorf("ok path: %+v", got)
	}
}

type chunkFetchStore struct {
	fakeStore
	mode string
}

func (c *chunkFetchStore) FetchChunksByDirPrefix(context.Context, int, string, int, int) ([]store.SearchResult, error) {
	switch c.mode {
	case "err":
		return nil, errors.New("boom")
	case "empty":
		return nil, nil
	default:
		return []store.SearchResult{{FilePath: "dep.go", Content: "chunk", StartLine: 1}}, nil
	}
}

func TestProcessBFSNode_edges(t *testing.T) {
	t.Parallel()
	p := bfsParams{
		seedPaths: map[string]bool{"seed.go": true},
		decay:     0.85,
		floor:     0.3,
		maxPaths:  1,
	}
	visited := map[string]float64{}
	expanded := map[string]float64{}
	node := bfsNode{path: "seed.go", depth: 0, score: 0.9}

	if processBFSNode("", node, p, visited, expanded) != nil {
		t.Error("empty neighbor")
	}
	// Score below floor
	low := bfsNode{path: "seed.go", depth: 0, score: 0.2}
	if processBFSNode("x.go", low, p, visited, expanded) != nil {
		t.Error("below floor")
	}
	// First visit expands
	next := processBFSNode("x.go", node, p, visited, expanded)
	if next == nil || expanded["x.go"] == 0 {
		t.Fatal("expected expand")
	}
	// maxPaths reached
	next = processBFSNode("y.go", node, p, visited, expanded)
	if next != nil {
		t.Error("maxPaths should block further expansion")
	}
}

// TestHybridSearch_topKClamp verifies that a caller-supplied topK above
// MaxTopK is clamped before reaching normaliseRRF (which would otherwise
// `make([]store.SearchResult, topK)` with the attacker's value).
func TestHybridSearch_topKClamp(t *testing.T) {
	const huge = 1_000_000
	st := &fakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "m"},
		simResults: []store.SearchResult{
			{FilePath: "a.go", Score: 0.9, StartLine: 1},
		},
		kwResults: []store.SearchResult{
			{FilePath: "b.go", Score: 0.5, StartLine: 2},
		},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1}, dims: 1})
	// huge topK must not panic / not OOM; the result length is bounded by
	// len(scoredList) (1+1=2) AND by MaxTopK (1000). Since 2 < 1000 the
	// clamp doesn't change behaviour here, but it MUST not propagate the
	// attacker value to normaliseRRF.
	got, err := svc.HybridSearch(context.Background(), 1, "q", "m", 1, huge, "")
	if err != nil {
		t.Fatalf("HybridSearch(huge): %v", err)
	}
	if len(got) > MaxTopK {
		t.Fatalf("topK not clamped: got %d results, MaxTopK=%d", len(got), MaxTopK)
	}
	// Negative topK must also be clamped to MaxTopK, not panic.
	if _, err := svc.HybridSearch(context.Background(), 1, "q", "m", 1, -1, ""); err != nil {
		t.Fatalf("HybridSearch(-1): %v", err)
	}
}
