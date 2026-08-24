package search

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// batchCountingStore records how the graph expansion fetched its chunks so the
// tests can assert one batch round trip instead of one query per file.
type batchCountingStore struct {
	store.Store
	batchCalls  int
	perPathCall int
	paths       []string
	batchErr    error
	chunks      map[string][]store.SearchResult
}

func (s *batchCountingStore) FetchChunksByPaths(_ context.Context, _ int, paths []string, _, _ int) (map[string][]store.SearchResult, error) {
	s.batchCalls++
	s.paths = append([]string(nil), paths...)
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	return s.chunks, nil
}

func (s *batchCountingStore) FetchChunksByDirPrefix(_ context.Context, _ int, path string, _, _ int) ([]store.SearchResult, error) {
	s.perPathCall++
	return s.chunks[path], nil
}

func expandedPaths() map[string]graphHop {
	return map[string]graphHop{
		"a.go": {Score: 0.85, Depth: 1},
		"b.go": {Score: 0.72, Depth: 2},
		"c.go": {Score: 0.61, Depth: 3},
	}
}

func TestFetchGraphChunksUsesOneBatchRoundTrip(t *testing.T) {
	st := &batchCountingStore{chunks: map[string][]store.SearchResult{
		"a.go": {{FilePath: "a.go", Content: "package a", StartLine: 1, EndLine: 1}},
		"b.go": {{FilePath: "b.go", Content: "package b", StartLine: 1, EndLine: 1}},
		// c.go has no indexed chunks — it must still surface as a placeholder.
	}}
	svc := NewService(st, nil)

	results := fetchGraphChunks(context.Background(), svc, 1, 3, expandedPaths())

	if st.batchCalls != 1 {
		t.Errorf("batch calls = %d, want exactly 1", st.batchCalls)
	}
	if st.perPathCall != 0 {
		t.Errorf("per-path calls = %d, want 0 when the store batches", st.perPathCall)
	}
	if len(st.paths) != 3 {
		t.Errorf("batched paths = %v, want all three expanded files", st.paths)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want two chunks plus one placeholder", len(results))
	}

	byPath := map[string]store.SearchResult{}
	for _, r := range results {
		byPath[r.FilePath] = r
		if r.Source != "graph" {
			t.Errorf("%s source = %q, want graph", r.FilePath, r.Source)
		}
	}
	if got := byPath["a.go"]; got.Score != 0.85 || got.GraphDepth != 1 {
		t.Errorf("a.go = %+v, want the hop score and depth applied", got)
	}
	if got := byPath["c.go"]; got.Content != "" || got.GraphDepth != 3 {
		t.Errorf("c.go = %+v, want an empty placeholder at depth 3", got)
	}
}

// A failing batch query must not fail the search: expansion is best-effort, so
// the per-path path takes over.
func TestFetchGraphChunksFallsBackWhenBatchFails(t *testing.T) {
	st := &batchCountingStore{
		batchErr: errors.New("batch query unsupported"),
		chunks: map[string][]store.SearchResult{
			"a.go": {{FilePath: "a.go", Content: "package a", StartLine: 1, EndLine: 1}},
		},
	}
	svc := NewService(st, nil)

	results := fetchGraphChunks(context.Background(), svc, 1, 3, expandedPaths())

	if st.batchCalls != 1 {
		t.Errorf("batch calls = %d, want 1 attempt", st.batchCalls)
	}
	if st.perPathCall != 3 {
		t.Errorf("per-path calls = %d, want one per expanded file after the fallback", st.perPathCall)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want one chunk plus two placeholders", len(results))
	}
}

// A store without the optional batch surface keeps the original behaviour.
type plainChunkStore struct {
	store.Store
	perPathCall int
}

func (s *plainChunkStore) FetchChunksByDirPrefix(_ context.Context, _ int, path string, _, _ int) ([]store.SearchResult, error) {
	s.perPathCall++
	return []store.SearchResult{{FilePath: path, Content: "x", StartLine: 1, EndLine: 1}}, nil
}

func TestFetchGraphChunksWithoutBatchSurface(t *testing.T) {
	st := &plainChunkStore{}
	svc := NewService(st, nil)

	results := fetchGraphChunks(context.Background(), svc, 1, 3, expandedPaths())

	if st.perPathCall != 3 {
		t.Errorf("per-path calls = %d, want one per expanded file", st.perPathCall)
	}
	if len(results) != 3 {
		t.Errorf("results = %d, want one per expanded file", len(results))
	}
}
