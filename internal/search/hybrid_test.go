package search

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func makeResult(path string, line int, score float64) store.SearchResult {
	return store.SearchResult{FilePath: path, StartLine: line, Score: score}
}

func TestChunkKey(t *testing.T) {
	t.Parallel()
	r := makeResult("src/main.go", 42, 0.9)
	if got := chunkKey(r); got != "src/main.go|42" {
		t.Fatalf("chunkKey = %q", got)
	}
}

func TestBuildRanks(t *testing.T) {
	t.Parallel()
	results := []store.SearchResult{
		makeResult("a.go", 10, 0.9),
		makeResult("b.go", 20, 0.8),
	}
	ranks := buildRanks(results)
	if ranks["a.go|10"] != 1 || ranks["b.go|20"] != 2 {
		t.Fatalf("buildRanks = %v", ranks)
	}
}

func TestFuseRRFDeduplicates(t *testing.T) {
	t.Parallel()
	// Same chunk appears in both vector and keyword results.
	vec := []store.SearchResult{makeResult("a.go", 10, 0.9)}
	kw := []store.SearchResult{makeResult("a.go", 10, 0.7)}
	vecRanks := buildRanks(vec)
	kwRanks := buildRanks(kw)
	scored := fuseRRF(vec, kw, vecRanks, kwRanks)
	if len(scored) != 1 {
		t.Fatalf("fuseRRF count = %d, want 1 (dedup)", len(scored))
	}
}

func TestFuseRRFKeywordOnlyAdds(t *testing.T) {
	t.Parallel()
	vec := []store.SearchResult{makeResult("a.go", 10, 0.9)}
	kw := []store.SearchResult{makeResult("b.go", 20, 0.8)}
	vecRanks := buildRanks(vec)
	kwRanks := buildRanks(kw)
	scored := fuseRRF(vec, kw, vecRanks, kwRanks)
	if len(scored) != 2 {
		t.Fatalf("fuseRRF count = %d, want 2", len(scored))
	}
}

func TestCompareScoredDesc(t *testing.T) {
	t.Parallel()
	a := rrfScored{score: 0.9}
	b := rrfScored{score: 0.5}
	c := rrfScored{score: 0.5}
	if compareScoredDesc(a, b) >= 0 {
		t.Fatal("a > b should be negative")
	}
	if compareScoredDesc(b, a) <= 0 {
		t.Fatal("b < a should be positive")
	}
	if compareScoredDesc(b, c) != 0 {
		t.Fatal("b == c should be zero")
	}
}

func TestNormaliseRRF(t *testing.T) {
	t.Parallel()
	scored := []rrfScored{
		{result: makeResult("a.go", 1, 0.0), score: 1.0},
		{result: makeResult("b.go", 2, 0.0), score: 0.5},
	}
	results := normaliseRRF(scored, 2)
	if results[0].FilePath != "a.go" || results[0].Score != 1.0 {
		t.Fatalf("top result = %+v", results[0])
	}
	if results[1].Score != 0.5 {
		t.Fatalf("second result score = %f", results[1].Score)
	}
}

func TestRerankRRFVectorOnly(t *testing.T) {
	t.Parallel()
	vec := []store.SearchResult{makeResult("a.go", 10, 1.0)}
	got := rerankRRF(vec, nil, 5)
	if len(got) != 1 || got[0].FilePath != "a.go" {
		t.Fatalf("rerankRRF vector-only = %v", got)
	}
}

func TestRerankRRFKeywordOnly(t *testing.T) {
	t.Parallel()
	kw := []store.SearchResult{makeResult("b.go", 20, 0.8)}
	got := rerankRRF(nil, kw, 5)
	if len(got) != 1 || got[0].FilePath != "b.go" {
		t.Fatalf("rerankRRF kw-only = %v", got)
	}
}

func TestRerankRRFBoth(t *testing.T) {
	t.Parallel()
	vec := []store.SearchResult{
		makeResult("a.go", 10, 0.0),
		makeResult("b.go", 20, 0.0),
	}
	kw := []store.SearchResult{
		makeResult("b.go", 20, 0.0),
		makeResult("c.go", 30, 0.0),
	}
	got := rerankRRF(vec, kw, 10)
	if len(got) < 3 {
		// Should have 3 unique chunks
		t.Fatalf("rerankRRF both = %d results, want 3", len(got))
	}
}

func TestRerankRRFTopK(t *testing.T) {
	t.Parallel()
	vec := []store.SearchResult{
		makeResult("a.go", 1, 0.0),
		makeResult("b.go", 2, 0.0),
		makeResult("c.go", 3, 0.0),
	}
	got := rerankRRF(vec, vec, 2)
	if len(got) != 2 {
		t.Fatalf("rerankRRF topK=2 got %d results", len(got))
	}
}

func TestRerankRRFZeroTopK(t *testing.T) {
	t.Parallel()
	vec := []store.SearchResult{makeResult("a.go", 1, 0.0)}
	got := rerankRRF(vec, nil, 0)
	if len(got) != 1 {
		t.Fatalf("rerankRRF topK=0 got %d", len(got))
	}
}

func TestRerankRRFBothEmpty(t *testing.T) {
	t.Parallel()
	got := rerankRRF(nil, nil, 10)
	if got != nil {
		t.Fatal("rerankRRF both empty should be nil")
	}
}
