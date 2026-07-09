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
func (s *Service) HybridSearch(ctx context.Context, projectID int, query string, model string, dims, topK int, worktree string) ([]store.SearchResult, error) {
	vec, err := s.emb.EmbedSingle(ctx, model, query)
	if err != nil {
		return nil, err
	}
	return s.hybridFuse(ctx, projectID, query, vec, dims, topK, worktree)
}

// hybridFuse merges vector and keyword ranked lists (caller supplies the query vector).
func (s *Service) hybridFuse(ctx context.Context, projectID int, query string, vec []float32, dims, topK int, worktree string) ([]store.SearchResult, error) {
	type result struct {
		results []store.SearchResult
		err     error
	}

	vecCh := make(chan result, 1)
	kwCh := make(chan result, 1)

	go func() {
		r, err := s.vectorSearch(ctx, projectID, vec, dims, topK, worktree)
		vecCh <- result{results: r, err: err}
	}()

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
	vecRanks := buildRanks(vector)
	kwRanks := buildRanks(keyword)

	// Collect all unique chunks with their fused RRF scores.
	scoredList := fuseRRF(vector, keyword, vecRanks, kwRanks)

	slices.SortFunc(scoredList, compareScoredDesc)

	if topK <= 0 || topK >= len(scoredList) {
		topK = len(scoredList)
	}
	return normaliseRRF(scoredList, topK)
}

// rrfScored pairs a search result with its fused RRF score.
type rrfScored struct {
	result store.SearchResult
	score  float64
}

// buildRanks returns a chunkKey -> 1-based rank map for the given results.
func buildRanks(results []store.SearchResult) map[string]int {
	ranks := make(map[string]int, len(results))
	for i, r := range results {
		ranks[chunkKey(r)] = i + 1
	}
	return ranks
}

// fuseRRF walks both ranked lists, deduplicates by chunk key, and assigns each
// unique chunk a fused RRF score (with a small bonus for overlapping hits).
func fuseRRF(vector, keyword []store.SearchResult, vecRanks, kwRanks map[string]int) []rrfScored {
	seen := make(map[string]bool, len(vector)+len(keyword))
	scoredList := make([]rrfScored, 0, len(vector)+len(keyword))

	for _, r := range vector {
		ck := chunkKey(r)
		seen[ck] = true
		score := 1.0 / (rrfK + float64(vecRanks[ck]))
		if kr, ok := kwRanks[ck]; ok {
			score += 1.0/(rrfK+float64(kr)) + rrfBonus
		}
		scoredList = append(scoredList, rrfScored{result: r, score: score})
	}

	for _, r := range keyword {
		ck := chunkKey(r)
		if seen[ck] {
			continue
		}
		scoredList = append(scoredList, rrfScored{
			result: r,
			score:  1.0 / (rrfK + float64(kwRanks[ck])),
		})
	}
	return scoredList
}

// compareScoredDesc sorts RRF scores descending; ties (within 1e-9) are stable.
func compareScoredDesc(a, b rrfScored) int {
	switch {
	case math.Abs(a.score-b.score) < 1e-9:
		return 0
	case a.score > b.score:
		return -1
	default:
		return 1
	}
}

// normaliseRRF takes the top-K scored results and scales scores to [0,1] so UX
// labels ("92%") are meaningful. The list must be sorted descending and non-empty.
func normaliseRRF(scoredList []rrfScored, topK int) []store.SearchResult {
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
