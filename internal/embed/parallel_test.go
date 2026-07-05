package embed

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// stubEmbedder records which model and inputs it received.
type stubEmbedder struct {
	name       string
	dims       int
	embedErr   error
	lastModel  string
	lastInputs []string
	callCount  int
}

func (s *stubEmbedder) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	return &ModelInfo{Name: s.name, Dims: s.dims}, nil
}

func (s *stubEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	s.callCount++
	s.lastModel = model
	s.lastInputs = inputs
	if s.embedErr != nil {
		return nil, s.embedErr
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		out[i] = []float32{float32(s.dims)} // dummy embedding
	}
	return out, nil
}

func (s *stubEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	s.callCount++
	s.lastModel = model
	if s.embedErr != nil {
		return nil, s.embedErr
	}
	return []float32{float32(s.dims)}, nil
}

func (s *stubEmbedder) ListModels(ctx context.Context) ([]string, error) {
	return []string{s.name}, nil
}

func TestParallelEmbedderRoundRobin(t *testing.T) {
	e1 := &stubEmbedder{name: "ollama-1", dims: 1024}
	e2 := &stubEmbedder{name: "ollama-2", dims: 1024}
	pool := NewParallelEmbedder([]Embedder{e1, e2})

	ctx := context.Background()

	// 3 calls → round-robin: e1, e2, e1
	for i := 0; i < 3; i++ {
		_, err := pool.Embed(ctx, "bge-m3", "hello")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if e1.callCount != 2 {
		t.Errorf("e1 calls = %d, want 2", e1.callCount)
	}
	if e2.callCount != 1 {
		t.Errorf("e2 calls = %d, want 1", e2.callCount)
	}
}

func TestParallelEmbedderModelInfo(t *testing.T) {
	e1 := &stubEmbedder{name: "ollama-1", dims: 1024}
	e2 := &stubEmbedder{name: "ollama-2", dims: 768}
	pool := NewParallelEmbedder([]Embedder{e1, e2})

	info, err := pool.ModelInfo(context.Background(), "bge-m3")
	if err != nil {
		t.Fatal(err)
	}
	if info.Dims != 1024 {
		t.Errorf("dims = %d, want 1024 (delegates to first entry)", info.Dims)
	}
}

func TestParallelEmbedderEmbedSingle(t *testing.T) {
	e1 := &stubEmbedder{name: "ollama-1", dims: 1024}
	e2 := &stubEmbedder{name: "ollama-2", dims: 1024}
	pool := NewParallelEmbedder([]Embedder{e1, e2})

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := pool.EmbedSingle(ctx, "bge-m3", "hello")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if e1.callCount != 2 {
		t.Errorf("e1 calls = %d, want 2", e1.callCount)
	}
	if e2.callCount != 1 {
		t.Errorf("e2 calls = %d, want 1", e2.callCount)
	}
}

// forceLocalFake always fails — used to test privacy fallback iterating.
type forceLocalFake struct {
	*stubEmbedder
}

func (f *forceLocalFake) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	if isForceLocal(ctx) {
		return nil, context.DeadlineExceeded // simulate non-local entry failing
	}
	return f.stubEmbedder.Embed(ctx, model, inputs...)
}

func TestParallelEmbedderPrivacyFallback(t *testing.T) {
	bad := &forceLocalFake{stubEmbedder: &stubEmbedder{name: "cloud", dims: 1024}}
	good := &stubEmbedder{name: "ollama", dims: 1024}
	pool := NewParallelEmbedder([]Embedder{bad, good})

	// First call lands on 'bad' (index 0). With force-local, it fails.
	// Fallback iterates to 'good' (index 1) which succeeds.
	ctx := WithForceLocal(context.Background(), true)
	res, err := pool.Embed(ctx, "bge-m3", "hello")
	if err != nil {
		t.Fatalf("privacy fallback failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(res))
	}
}

// When every entry fails in privacy mode, the returned error must wrap the real
// underlying failure — not nil. Guards against the lastErr shadowing regression.
func TestParallelEmbedderPrivacyFallbackAllFail(t *testing.T) {
	bad1 := &forceLocalFake{stubEmbedder: &stubEmbedder{name: "cloud-1", dims: 1024}}
	bad2 := &forceLocalFake{stubEmbedder: &stubEmbedder{name: "cloud-2", dims: 1024}}
	pool := NewParallelEmbedder([]Embedder{bad1, bad2})

	ctx := WithForceLocal(context.Background(), true)
	_, err := pool.Embed(ctx, "bge-m3", "hello")
	if err == nil {
		t.Fatal("expected an error when all entries fail")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error must wrap the underlying failure, got: %v", err)
	}
}

func TestNewParallelEmbedder(t *testing.T) {
	e1 := &stubEmbedder{name: "a", dims: 768}
	pool := NewParallelEmbedder([]Embedder{e1})
	if pool == nil {
		t.Fatal("NewParallelEmbedder returned nil")
	}
	if len(pool.entries) != 1 {
		t.Errorf("entries = %d, want 1", len(pool.entries))
	}
}

func TestParallelEmbedderListModels(t *testing.T) {
	e1 := &stubEmbedder{name: "bge-m3", dims: 1024}
	pool := NewParallelEmbedder([]Embedder{e1})

	models, err := pool.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(models, []string{"bge-m3"}) {
		t.Errorf("models = %v, want [bge-m3]", models)
	}
}

func TestParallelEmbedderSingleEntry(t *testing.T) {
	e1 := &stubEmbedder{name: "solo", dims: 768}
	pool := NewParallelEmbedder([]Embedder{e1})

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := pool.Embed(ctx, "m", "x")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// Single entry → always goes to e1
	if e1.callCount != 5 {
		t.Errorf("callCount = %d, want 5", e1.callCount)
	}
}
