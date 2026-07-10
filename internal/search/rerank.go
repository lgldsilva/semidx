package search

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/lgldsilva/semidx/internal/store"
)

// Reranker re-scores and reorders the top-K results for a query (REQ-SRCH-11).
// It is best-effort: implementations must return a usable slice (typically the
// input reordered) and never fail the search. A nil Reranker on the Service
// disables reranking entirely, so the default path pays nothing.
type Reranker interface {
	Rerank(ctx context.Context, query string, results []store.SearchResult) []store.SearchResult
}

// SetReranker installs (or clears, with nil) the Service's optional reranker.
func (s *Service) SetReranker(r Reranker) { s.reranker = r }

// LexicalReranker blends each result's original score with the fraction of
// query terms that appear in the chunk content. It is a dependency-free,
// deterministic stand-in for a cross-encoder: it cannot model semantics beyond
// the vector score already captured, but it reliably lifts results that contain
// the exact query terms — most valuable in keyword mode, where every result
// carries the same placeholder score and the vector order gives no signal.
//
// Weight is the lexical contribution in [0,1]; the blended key is
// (1-Weight)*score + Weight*overlap. The original SearchResult.Score is left
// untouched so formatters keep showing the true similarity; only the order
// changes.
type LexicalReranker struct {
	Weight float64
}

// NewLexicalReranker returns a LexicalReranker, clamping weight to [0,1].
func NewLexicalReranker(weight float64) *LexicalReranker {
	switch {
	case weight < 0:
		weight = 0
	case weight > 1:
		weight = 1
	}
	return &LexicalReranker{Weight: weight}
}

func (r *LexicalReranker) Rerank(_ context.Context, query string, results []store.SearchResult) []store.SearchResult {
	terms := tokenize(query)
	if len(terms) == 0 || len(results) < 2 {
		return results
	}
	type keyed struct {
		res   store.SearchResult
		blend float64
	}
	scored := make([]keyed, len(results))
	for i, res := range results {
		content := strings.ToLower(res.Content)
		hit := 0
		for t := range terms {
			if strings.Contains(content, t) {
				hit++
			}
		}
		overlap := float64(hit) / float64(len(terms))
		scored[i] = keyed{res: res, blend: (1-r.Weight)*res.Score + r.Weight*overlap}
	}
	// Stable sort by blended score descending so equal blends preserve the input
	// (semantic) order.
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].blend > scored[j].blend })
	out := make([]store.SearchResult, len(scored))
	for i := range scored {
		out[i] = scored[i].res
	}
	return out
}

// tokenize lowercases query and splits on non-alphanumeric runes, keeping tokens
// of length >= 2 as a set (deduplicated) so repeated words don't skew overlap.
func tokenize(query string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	set := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		if len(f) >= 2 {
			set[f] = struct{}{}
		}
	}
	return set
}
