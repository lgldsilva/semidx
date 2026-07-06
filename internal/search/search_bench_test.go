package search

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

// ---------------------------------------------------------------------------
// Fakes (same pattern as service_test.go, self-contained in this file)
// ---------------------------------------------------------------------------

// benchFakeStore implements store.IndexStore methods that Service.Search calls.
type benchFakeStore struct {
	store.Store
	project    *store.Project
	getErr     error
	simResults []store.SearchResult
	kwResults  []store.SearchResult
}

func (f *benchFakeStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.project, nil
}

func (f *benchFakeStore) SearchSimilar(_ context.Context, _ int, _ []float32, _, topK int) ([]store.SearchResult, error) {
	return f.simResults, nil
}

func (f *benchFakeStore) SearchSimilarKeywords(_ context.Context, _ int, _ string, _, topK int) ([]store.SearchResult, error) {
	return f.kwResults, nil
}

// benchFakeEmbedder implements embed.Embedder with fast no-op returns.
type benchFakeEmbedder struct {
	embed.Embedder
	vec      []float32
	embedErr error
	dims     int
}

func (f *benchFakeEmbedder) ModelInfo(_ context.Context, model string) (*embed.ModelInfo, error) {
	if f.dims == 0 {
		return nil, io.ErrUnexpectedEOF // no model info available
	}
	return &embed.ModelInfo{Name: model, Dims: f.dims}, nil
}

func (f *benchFakeEmbedder) EmbedSingle(_ context.Context, _, _ string) ([]float32, error) {
	return f.vec, f.embedErr
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

// BenchmarkServiceSearch_VectorPath benchmarks the vector search path: the
// embedder succeeds and the store returns results.
func BenchmarkServiceSearch_VectorPath(b *testing.B) {
	b.ReportAllocs()
	st := &benchFakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		simResults: []store.SearchResult{
			{FilePath: "a.go", Content: "func hello()", Score: 0.92, StartLine: 1, EndLine: 3},
		},
	}
	emb := &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	ctx := context.Background()
	req := Request{Project: "p", Query: "find the hello function", TopK: 10}
	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, req)
	}
}

// BenchmarkServiceSearch_FallbackPath benchmarks the keyword fallback path:
// the embedder fails and the store falls through to keyword search.
func BenchmarkServiceSearch_FallbackPath(b *testing.B) {
	b.ReportAllocs()
	st := &benchFakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		kwResults: []store.SearchResult{
			{FilePath: "b.go", Content: "func goodbye()", Score: 0.5, StartLine: 5, EndLine: 7},
		},
	}
	emb := &benchFakeEmbedder{embedErr: io.ErrUnexpectedEOF, dims: 3}
	svc := NewService(st, emb)

	ctx := context.Background()
	req := Request{Project: "p", Query: "goodbye", TopK: 5}
	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, req)
	}
}

// BenchmarkServiceSearch_KeywordOnly benchmarks the keyword-only path that
// skips embedding entirely.
func BenchmarkServiceSearch_KeywordOnly(b *testing.B) {
	b.ReportAllocs()
	st := &benchFakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		kwResults: []store.SearchResult{
			{FilePath: "c.go", Content: "func process()", Score: 0.5, StartLine: 10, EndLine: 12},
		},
	}
	emb := &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	ctx := context.Background()
	req := Request{Project: "p", Query: "process", TopK: 5, KeywordOnly: true}
	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, req)
	}
}

// BenchmarkHumanFormat benchmarks the HumanFormatter output renderer with a
// full 100-result response.
func BenchmarkHumanFormat(b *testing.B) {
	b.ReportAllocs()
	resp := &Response{
		Model:   "bge-m3",
		Results: make([]store.SearchResult, 100),
	}
	for i := range resp.Results {
		resp.Results[i] = store.SearchResult{
			FilePath:  "internal/server/server.go",
			StartLine: i + 1,
			EndLine:   i + 3,
			Score:     0.75 + float64(i)*0.001,
			Content:   strings.Repeat("x", 200),
		}
	}
	f := HumanFormatter{Preview: 200}
	b.ResetTimer()
	for b.Loop() {
		_ = f.Format(io.Discard, resp)
	}
}
