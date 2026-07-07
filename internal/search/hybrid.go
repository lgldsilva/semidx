package search

import (
	"context"
	"math"
	"slices"

	"github.com/lgldsilva/semidx/internal/store"
)

const (
	// rrfK is the constant used in the RRF formula: score = 1/(k + rank).
	rrfK = 60.0
	// rrfBonus is added to a result's score when it appears in both lists.
	rrfBonus = 0.1
)

// HybridSearch runs vector search and keyword search concurrently,
// merges results using Reciprocal Rank Fusion, and returns the top K.
// It uses the provided embedder for the vector branch.
func (s *Service) HybridSearch(ctx context.Context, projectID int, query string, model string, dims, topK int, worktree string) ([]store.SearchResult, error) {
	type result struct {
		results []store.SearchResult
		err     error
	}

	vecCh := make(chan result, 1)
	kwCh := make(chan result, 1)

	// Run vector search in a goroutine.
	go func() {
		vec, err := s.emb.EmbedSingle(ctx, model, query)
		if err != nil {
			vecCh <- result{err: err}
			return
		}
		r, err := s.vectorSearch(ctx, projectID, vec, dims, topK, worktree)
		vecCh <- result{results: r, err: err}
	}()

	// Run keyword search in a goroutine.
	go func() {
		r, err := s.keywordSearch(ctx, projectID, query, dims, topK, worktree)
		kwCh <- result{results: r, err: err}
	}()

	vres := <-vecCh
	if vres.err != nil {
		return nil, vres.err
	}
	kres := <-kwCh
	if kres.err != nil {
		// Keyword search failure is non-fatal — return vector results alone.
		return vres.results, nil
	}

	return rerankRRF(vres.results, kres.results, topK), nil
}

// rerankRRF merges two ranked result lists using Reciprocal Rank Fusion.
// Results appearing in both lists get a bonus. The returned list is sorted by
// RRF score descending, capped at topK.
func rerankRRF(vector, keyword []store.SearchResult, topK int) []store.SearchResult {
	if len(vector) == 0 {
		return keyword
	}
	if len(keyword) == 0 {
		return vector
	}

	// Build position maps: path -> rank (1-based).
	vecRanks := make(map[string]int, len(vector))
	for i, r := range vector {
		vecRanks[r.FilePath] = i + 1
	}
	kwRanks := make(map[string]int, len(keyword))
	for i, r := range keyword {
		kwRanks[r.FilePath] = i + 1
	}

	// Collect all unique file paths.
	seen := make(map[string]bool)
	type scored struct {
		result store.SearchResult
		score  float64
	}

	var scoredList []scored

	for _, r := range vector {
		seen[r.FilePath] = true
		vr := vecRanks[r.FilePath]
		kr, inKW := kwRanks[r.FilePath]
		score := 1.0/(rrfK+float64(vr)) + 1.0/(rrfK+float64(kr))
		if inKW {
			score += rrfBonus
		}
		scoredList = append(scoredList, scored{result: r, score: score})
	}

	for _, r := range keyword {
		if seen[r.FilePath] {
			continue
		}
		kr := kwRanks[r.FilePath]
		score := 1.0 / (rrfK + float64(kr))
		scoredList = append(scoredList, scored{result: r, score: score})
	}

	// Sort by RRF score descending.
	slices.SortFunc(scoredList, func(a, b scored) int {
		switch {
		case math.Abs(a.score-b.score) < 1e-9:
			return 0
		case a.score > b.score:
			return -1
		default:
			return 1
		}
	})

	if topK <= 0 || topK >= len(scoredList) {
		topK = len(scoredList)
	}
	out := make([]store.SearchResult, topK)
	for i := 0; i < topK; i++ {
		out[i] = scoredList[i].result
		out[i].Score = scoredList[i].score
	}
	return out
}
