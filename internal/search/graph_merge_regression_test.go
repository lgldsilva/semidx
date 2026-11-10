package search

import (
	"context"
	"fmt"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// Regression: graph-expanded neighbours were scored maxSeed·0.85^d, so with a
// normalised top-1 seed (1.0) a depth-1 neighbour (0.85) outranked direct hits
// 2..N. A neighbour must never rank above any direct hit.
func TestMergeGraphResults_neighboursNeverOutrankDirectHits(t *testing.T) {
	t.Parallel()
	original := []store.SearchResult{
		{FilePath: "a.go", Score: 1.0},
		{FilePath: "b.go", Score: 0.6},
		{FilePath: "c.go", Score: 0.4},
	}
	expanded := []store.SearchResult{
		{FilePath: "n2.go", Score: 0.72, Source: "graph", GraphDepth: 2},
		{FilePath: "n1.go", Score: 0.85, Source: "graph", GraphDepth: 1},
	}
	merged := mergeGraphResults(original, expanded, 5)
	want := []string{"a.go", "b.go", "c.go", "n1.go", "n2.go"}
	if len(merged) != len(want) {
		t.Fatalf("len = %d, want %d: %+v", len(merged), len(want), merged)
	}
	for i, r := range merged {
		if r.FilePath != want[i] {
			t.Errorf("merged[%d] = %s (%.3f), want %s", i, r.FilePath, r.Score, want[i])
		}
		if i > 0 && r.Score > merged[i-1].Score {
			t.Errorf("merged[%d].Score %.3f > merged[%d].Score %.3f", i, r.Score, i-1, merged[i-1].Score)
		}
	}
	if merged[3].Score >= 0.4 {
		t.Errorf("depth-1 neighbour score %.3f must be below the weakest direct hit 0.4", merged[3].Score)
	}
}

// Regression: nothing truncated after the merge, so top_k=5 with graph=true
// returned every expanded file (up to maxGraphExpandPaths). The merge keeps all
// direct hits plus at most topK graph chunks.
func TestMergeGraphResults_capsExpansionAtTopK(t *testing.T) {
	t.Parallel()
	original := []store.SearchResult{{FilePath: "a.go", Score: 0.9}, {FilePath: "b.go", Score: 0.8}}
	expanded := make([]store.SearchResult, 0, maxGraphExpandPaths)
	for i := range maxGraphExpandPaths {
		expanded = append(expanded, store.SearchResult{FilePath: fmt.Sprintf("n%03d.go", i), Source: "graph", GraphDepth: 1 + i%2})
	}
	merged := mergeGraphResults(original, expanded, 5)
	if len(merged) != len(original)+5 {
		t.Fatalf("len = %d, want %d (direct hits + topK graph chunks)", len(merged), len(original)+5)
	}
	for _, r := range merged[len(original):] {
		if r.GraphDepth != 1 {
			t.Errorf("cut kept depth-%d %s before exhausting depth-1 hops", r.GraphDepth, r.FilePath)
		}
	}
}

// Regression: the 0.3 floor applied to absolute scores, so keyword-fallback
// seeds (bm25/LIKE scores far below 0.3) discarded every expansion.
func TestExpandByGraph_lowKeywordSeedScoresStillExpand(t *testing.T) {
	g := newGraphFixture(t)
	ctx := context.Background()
	for _, p := range []string{"a.go", "b.go", "c.go"} {
		g.createFile(ctx, t, p, "package p\nfunc F() {}")
	}
	g.addEdge(ctx, t, "a.go", "b.go")
	g.addEdge(ctx, t, "b.go", "c.go")

	seeds := []store.SearchResult{{FilePath: "a.go", Content: "a", Score: 0.000002, Source: "keyword"}}
	results, err := g.svc.expandByGraph(ctx, &Request{GraphMaxDepth: 2}, seeds, g.pid, 1)
	if err != nil {
		t.Fatalf("expandByGraph: %v", err)
	}
	got := collectPaths(results)
	if !hasPath(got, "b.go") || !hasPath(got, "c.go") {
		t.Errorf("keyword-scored seed lost its expansion: %v", got)
	}

	// The in-memory BFS fallback must behave the same.
	bfs := runGraphBFS(bfsParams{
		graph:    map[string][]string{"a.go": {"b.go"}, "b.go": {"c.go"}},
		reverse:  map[string][]string{},
		seeds:    seeds,
		maxDepth: 2, decay: graphExpandDecay, floor: graphExpandFloor, maxPaths: maxGraphExpandPaths,
		seedPaths: map[string]bool{"a.go": true},
	})
	if _, ok := bfs["b.go"]; !ok {
		t.Errorf("BFS fallback dropped depth-1 neighbour of a low-scored seed: %v", bfs)
	}
}
