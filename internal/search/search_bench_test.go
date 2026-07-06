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
	project       *store.Project
	getErr        error
	simResults    []store.SearchResult
	kwResults     []store.SearchResult
	graphEdges    map[string][]string // source -> targets
	chunksByPath  map[string][]store.SearchResult
	insertCalled  int
}

func (f *benchFakeStore) GetProject(_ context.Context, name string) (*store.Project, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.project, nil
}

func (f *benchFakeStore) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.project, nil
}

func (f *benchFakeStore) GetProjectByID(_ context.Context, _ int) (*store.Project, error) {
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

func (f *benchFakeStore) FetchGraphNeighbors(_ context.Context, _ int) (map[string][]string, error) {
	return f.graphEdges, nil
}

func (f *benchFakeStore) FetchChunksByPath(_ context.Context, _ int, filePath string, _, _ int) ([]store.SearchResult, error) {
	return f.chunksByPath[filePath], nil
}

func (f *benchFakeStore) InsertFileDependencies(_ context.Context, _ int, _ string, _ []string) error {
	f.insertCalled++
	return nil
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

// BenchmarkGraphRAG_Disabled measures search latency WITHOUT graph expansion.
// This is the baseline — traditional semantic/keyword search.
func BenchmarkGraphRAG_Disabled(b *testing.B) {
	b.ReportAllocs()
	st := &benchFakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		kwResults: []store.SearchResult{
			{FilePath: "gateway/gateway.go", Content: "func HandleRequest", Score: 0.92, StartLine: 1, EndLine: 10},
		},
		// Graph edges exist but won't be used (Graph=false).
		graphEdges: map[string][]string{
			"gateway/gateway.go": {"auth/auth.go", "log/log.go"},
			"auth/auth.go":       {"jwt/jwt.go", "log/log.go"},
		},
		chunksByPath: map[string][]store.SearchResult{
			"auth/auth.go": {{FilePath: "auth/auth.go", Content: "func CheckCredential", Score: 0.5, StartLine: 1, EndLine: 10}},
			"jwt/jwt.go":   {{FilePath: "jwt/jwt.go", Content: "func DecodeAndCompare", Score: 0.5, StartLine: 1, EndLine: 15}},
		},
	}
	emb := &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	ctx := context.Background()
	req := Request{Project: "p", Query: "validate tokens", TopK: 5, Graph: false}
	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, req)
	}
}

// BenchmarkGraphRAG_Enabled measures search latency WITH graph expansion.
// This shows the overhead of BFS expansion + neighbor chunk fetching.
func BenchmarkGraphRAG_Enabled(b *testing.B) {
	b.ReportAllocs()
	st := &benchFakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		kwResults: []store.SearchResult{
			{FilePath: "gateway/gateway.go", Content: "func HandleRequest", Score: 0.92, StartLine: 1, EndLine: 10},
		},
		graphEdges: map[string][]string{
			"gateway/gateway.go": {"auth/auth.go", "log/log.go"},
			"auth/auth.go":       {"jwt/jwt.go", "log/log.go"},
		},
		chunksByPath: map[string][]store.SearchResult{
			"auth/auth.go": {{FilePath: "auth/auth.go", Content: "func CheckCredential", Score: 0.5, StartLine: 1, EndLine: 10}},
			"jwt/jwt.go":   {{FilePath: "jwt/jwt.go", Content: "func DecodeAndCompare", Score: 0.5, StartLine: 1, EndLine: 15}},
		},
	}
	emb := &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	ctx := context.Background()
	req := Request{Project: "p", Query: "validate tokens", TopK: 5, Graph: true, GraphMaxDepth: 2}
	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, req)
	}
}

// BenchmarkGraphRAG_DeepGraph measures graph expansion with a deeper dependency
// chain (5 levels) to stress-test BFS performance.
func BenchmarkGraphRAG_DeepGraph(b *testing.B) {
	b.ReportAllocs()
	// Build a 5-level chain: a→b→c→d→e, plus each imports a hub.
	edges := map[string][]string{
		"a/a.go": {"b/b.go", "log/log.go"},
		"b/b.go": {"c/c.go", "log/log.go"},
		"c/c.go": {"d/d.go", "log/log.go"},
		"d/d.go": {"e/e.go", "log/log.go"},
		"e/e.go": {"log/log.go"},
	}
	chunks := make(map[string][]store.SearchResult)
	for p := range edges {
		chunks[p] = []store.SearchResult{{
			FilePath: p, Content: "content for " + p, Score: 0.5, StartLine: 1, EndLine: 5,
		}}
	}

	st := &benchFakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		kwResults: []store.SearchResult{
			{FilePath: "a/a.go", Content: "entry point", Score: 0.95, StartLine: 1, EndLine: 10},
		},
		graphEdges:   edges,
		chunksByPath: chunks,
	}
	emb := &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	ctx := context.Background()
	req := Request{Project: "p", Query: "entry point processing", TopK: 5, Graph: true, GraphMaxDepth: 5}
	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, req)
	}
}

// BenchmarkGraphRAG_WideGraph measures graph expansion with many neighbors per
// node (fan-out stress test — simulates a hub-like scenario with decay filtering).
func BenchmarkGraphRAG_WideGraph(b *testing.B) {
	b.ReportAllocs()
	// 1 seed → 20 neighbors → each has 5 sub-neighbors = 100 potential hits.
	edges := map[string][]string{
		"main/main.go": nil,
	}
	for i := range 20 {
		neighbor := "pkg" + itoa(i) + "/file.go"
		edges["main/main.go"] = append(edges["main/main.go"], neighbor)
		edges[neighbor] = nil
		for j := range 5 {
			sub := "pkg" + itoa(i) + "/sub" + itoa(j) + ".go"
			edges[neighbor] = append(edges[neighbor], sub)
			edges[sub] = nil
		}
	}
	chunks := make(map[string][]store.SearchResult)
	for p := range edges {
		chunks[p] = []store.SearchResult{{
			FilePath: p, Content: "content", Score: 0.5, StartLine: 1, EndLine: 3,
		}}
	}

	st := &benchFakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "bge-m3"},
		kwResults: []store.SearchResult{
			{FilePath: "main/main.go", Content: "main entry", Score: 0.95, StartLine: 1, EndLine: 10},
		},
		graphEdges:   edges,
		chunksByPath: chunks,
	}
	emb := &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	ctx := context.Background()
	req := Request{Project: "p", Query: "main entry", TopK: 5, Graph: true, GraphMaxDepth: 2}
	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, req)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	pos := len(buf)
	n := i
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
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
