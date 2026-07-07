package search

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/lgldsilva/semidx/internal/store"
)

const (
	// rrfK is the constant used in the RRF formula: score = 1/(k + rank).
	rrfK = 60.0
	// rrfBonus is added to a result's score when it appears in both lists.
	// Kept small (~1/k) so a rank-1 single-list hit beats a rank-60 overlap.
	rrfBonus = 0.02
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
// Results are keyed by FilePath+StartLine (chunk-level dedup) rather than
// FilePath alone, so multi-chunk files get proper per-chunk ranking.
// RRF scores are normalised to [0,1] for compatible UX labels.
func rerankRRF(vector, keyword []store.SearchResult, topK int) []store.SearchResult {
	if len(vector) == 0 {
		return keyword
	}
	if len(keyword) == 0 {
		return vector
	}

	// Build position maps: chunkKey -> rank (1-based).
	vecRanks := make(map[string]int, len(vector))
	for i, r := range vector {
		vecRanks[chunkKey(r)] = i + 1
	}
	kwRanks := make(map[string]int, len(keyword))
	for i, r := range keyword {
		kwRanks[chunkKey(r)] = i + 1
	}

	// Collect all unique chunks.
	seen := make(map[string]bool)
	type scored struct {
		result store.SearchResult
		score  float64
	}

	var scoredList []scored

	for _, r := range vector {
		ck := chunkKey(r)
		seen[ck] = true
		vr := vecRanks[ck]
		score := 1.0 / (rrfK + float64(vr))
		kr, inKW := kwRanks[ck]
		if inKW {
			score += 1.0/(rrfK+float64(kr)) + rrfBonus
		}
		scoredList = append(scoredList, scored{result: r, score: score})
	}

	for _, r := range keyword {
		ck := chunkKey(r)
		if seen[ck] {
			continue
		}
		kr := kwRanks[ck]
		score := 1.0 / (rrfK + float64(kr))
		scoredList = append(scoredList, scored{result: r, score: score})
	}

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

	// Normalise scores to [0,1] so UX labels ("92%") are meaningful.
	maxScore := scoredList[0].score
	out := make([]store.SearchResult, topK)
	for i := 0; i < topK; i++ {
		out[i] = scoredList[i].result
		if maxScore > 0 {
			out[i].Score = scoredList[i].score / maxScore
		}
	}
	return out
}

// chunkKey returns a stable key for a chunk: file path + start line.
func chunkKey(r store.SearchResult) string {
	return fmt.Sprintf("%s|%d", r.FilePath, r.StartLine)
}
