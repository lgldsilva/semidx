package search

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestApplyRankedDiversityClampsCallerTopK(t *testing.T) {
	results := make([]rankedResult, MaxTopK+25)
	for i := range results {
		results[i] = rankedResult{project: "project", result: store.SearchResult{FilePath: string(rune('a'+i%26)) + ".go"}}
	}
	got := applyRankedDiversity(results, 0, 0, MaxTopK+1000000)
	if len(got) != MaxTopK {
		t.Fatalf("got %d results, want MaxTopK=%d", len(got), MaxTopK)
	}
	if clampMultiTopK(0) != 5 || clampMultiTopK(-1) != 5 {
		t.Error("non-positive topK should use the normal default")
	}
}
