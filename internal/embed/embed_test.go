package embed

import (
	"context"
	"errors"
	"testing"
)

// fakeEmbedder records calls and returns a fixed result or error.
type fakeEmbedder struct {
	name   string
	fail   bool
	calls  *[]string // shared call log across providers
	single []float32
}

func (f *fakeEmbedder) log() { *f.calls = append(*f.calls, f.name) }
func (f *fakeEmbedder) ModelInfo(ctx context.Context, model string) (*ModelInfo, error) {
	f.log()
	if f.fail {
		return nil, errors.New("boom")
	}
	return &ModelInfo{Name: model, Dims: 3}, nil
}
func (f *fakeEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	f.log()
	if f.fail {
		return nil, errors.New("boom")
	}
	return [][]float32{f.single}, nil
}
func (f *fakeEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	f.log()
	if f.fail {
		return nil, errors.New("boom")
	}
	return f.single, nil
}
func (f *fakeEmbedder) ListModels(ctx context.Context) ([]string, error) {
	f.log()
	if f.fail {
		return nil, errors.New("boom")
	}
	return []string{f.name + "-model"}, nil
}

func TestChainFallsBackOnError(t *testing.T) {
	var calls []string
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "remote", Local: false, Embedder: &fakeEmbedder{name: "remote", fail: true, calls: &calls}},
		{Name: "local", Local: true, Embedder: &fakeEmbedder{name: "local", single: []float32{1, 2, 3}, calls: &calls}},
	}, false)

	got, err := chain.EmbedSingle(context.Background(), "m", "hi")
	if err != nil {
		t.Fatalf("EmbedSingle error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %v, want 3-dim vector", got)
	}
	if len(calls) != 2 || calls[0] != "remote" || calls[1] != "local" {
		t.Errorf("call order = %v, want [remote local]", calls)
	}
}

func TestChainPrivacySkipsRemote(t *testing.T) {
	var calls []string
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "remote", Local: false, Embedder: &fakeEmbedder{name: "remote", calls: &calls}},
		{Name: "local", Local: true, Embedder: &fakeEmbedder{name: "local", single: []float32{9}, calls: &calls}},
	}, true) // privacy on

	if _, err := chain.EmbedSingle(context.Background(), "m", "hi"); err != nil {
		t.Fatalf("EmbedSingle error: %v", err)
	}
	if len(calls) != 1 || calls[0] != "local" {
		t.Errorf("privacy mode calls = %v, want only [local]", calls)
	}
}

func TestChainForceLocalViaContext(t *testing.T) {
	var calls []string
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "remote", Local: false, Embedder: &fakeEmbedder{name: "remote", calls: &calls}},
		{Name: "local", Local: true, Embedder: &fakeEmbedder{name: "local", single: []float32{7}, calls: &calls}},
	}, false)

	ctx := WithForceLocal(context.Background(), true)
	if _, err := chain.EmbedSingle(ctx, "m", "hi"); err != nil {
		t.Fatalf("EmbedSingle error: %v", err)
	}
	if len(calls) != 1 || calls[0] != "local" {
		t.Errorf("force-local calls = %v, want only [local]", calls)
	}
}

func TestChainAllFail(t *testing.T) {
	var calls []string
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "a", Local: true, Embedder: &fakeEmbedder{name: "a", fail: true, calls: &calls}},
	}, false)
	if _, err := chain.EmbedSingle(context.Background(), "m", "hi"); err == nil {
		t.Error("expected error when all providers fail")
	}
}

func TestChainListModelsDedupes(t *testing.T) {
	var calls []string
	same := func(n string) *fakeEmbedder { return &fakeEmbedder{name: "dup", calls: &calls} }
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "a", Local: true, Embedder: same("a")},
		{Name: "b", Local: true, Embedder: same("b")},
	}, false)
	models, err := chain.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels error: %v", err)
	}
	if len(models) != 1 || models[0] != "dup-model" {
		t.Errorf("models = %v, want deduped [dup-model]", models)
	}
}

func TestInferDims(t *testing.T) {
	cases := map[string]int{
		"nomic-embed-text":       768,
		"bge-m3":                 1024,
		"mxbai-embed-large":      1024,
		"gemini-embedding-2":     3072,
		"text-embedding-3-large": 3072,
		"unknown-model":          0,
	}
	for model, want := range cases {
		if got := InferDims(model); got != want {
			t.Errorf("InferDims(%q) = %d, want %d", model, got, want)
		}
	}
}
