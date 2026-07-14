package embed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// --- OpenAIClient -----------------------------------------------------------

func TestOpenAIEmbedSuccess(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2,0.3]},{"embedding":[0.4,0.5,0.6]}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "sk-test")
	got, err := c.Embed(context.Background(), "text-embedding-3-small", "alpha", "beta")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 2 || len(got[0]) != 3 || got[1][2] != 0.6 {
		t.Fatalf("embeddings = %v", got)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", gotAuth)
	}
	if gotPath != "/embeddings" {
		t.Errorf("path = %q, want /embeddings", gotPath)
	}
}

func TestOpenAIEmbedNoInputs(t *testing.T) {
	c := NewOpenAIClient("http://example.invalid", "k")
	if _, err := c.Embed(context.Background(), "m"); err == nil {
		t.Error("Embed with no inputs should error")
	}
}

func TestOpenAIEmbedHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "k")
	_, err := c.Embed(context.Background(), "m", "x")
	if err == nil || !strings.Contains(err.Error(), "openai embed failed") {
		t.Fatalf("err = %v, want openai embed failed", err)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err should include server body: %v", err)
	}
}

func TestOpenAIEmbedMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "k")
	if _, err := c.Embed(context.Background(), "m", "x"); err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestOpenAIEmbedRequestBuildError(t *testing.T) {
	// A malformed base URL makes http.NewRequestWithContext fail before any I/O.
	c := NewOpenAIClient("://bad-url", "k")
	if _, err := c.Embed(context.Background(), "m", "x"); err == nil {
		t.Error("invalid URL should fail request construction")
	}
}

func TestOpenAIEmbedTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening → connection refused, no hang

	c := NewOpenAIClient(url, "k")
	if _, err := c.Embed(context.Background(), "m", "x"); err == nil {
		t.Error("transport error (server closed) should propagate")
	}
}

func TestOpenAISingleAndMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,2,3,4]}]}`))
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "k")
	vec, err := c.EmbedSingle(context.Background(), "text-embedding-3-large", "hello")
	if err != nil || len(vec) != 4 {
		t.Fatalf("EmbedSingle = %v, err %v", vec, err)
	}

	info, err := c.ModelInfo(context.Background(), "text-embedding-3-large")
	if err != nil || info.Dims != 3072 || info.Name != "text-embedding-3-large" {
		t.Fatalf("ModelInfo = %+v, err %v", info, err)
	}

	models, err := c.ListModels(context.Background())
	if err != nil || len(models) == 0 {
		t.Fatalf("ListModels = %v, err %v", models, err)
	}
}

func TestOpenAISingleErrorPaths(t *testing.T) {
	// Empty data → "no embedding returned".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	c := NewOpenAIClient(srv.URL, "k")
	if _, err := c.EmbedSingle(context.Background(), "m", "x"); err == nil {
		t.Error("empty data should yield 'no embedding returned'")
	}

	// Underlying Embed error propagates through EmbedSingle.
	bad := NewOpenAIClient("://bad", "k")
	if _, err := bad.EmbedSingle(context.Background(), "m", "x"); err == nil {
		t.Error("EmbedSingle should propagate Embed error")
	}
}

func TestOpenAIModelInfoUnknownModelErrors(t *testing.T) {
	c := NewOpenAIClient("http://example.invalid", "k")
	_, err := c.ModelInfo(context.Background(), "totally-unknown")
	if err == nil || !strings.Contains(err.Error(), "unknown embedding model") {
		t.Fatalf("err = %v, want unknown embedding model", err)
	}
}

func TestOpenAIListModelsFromEndpoint(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"text-embedding-3-small"},
			{"id":"gpt-4o"},
			{"id":"custom-Embedding-x"}
		]}`))
	}))
	defer srv.Close()

	c := NewOpenAIClient(srv.URL, "sk-test")
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	want := []string{"text-embedding-3-small", "custom-Embedding-x"}
	if len(models) != 2 || models[0] != want[0] || models[1] != want[1] {
		t.Errorf("models = %v, want %v (only embedding ids)", models, want)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", gotAuth)
	}
}

func TestOpenAIListModelsFallsBackToStatic(t *testing.T) {
	// Error, non-200, malformed JSON, and no-embedding-ids responses all fall
	// back to the static catalog.
	cases := map[string]http.HandlerFunc{
		"http error": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusNotFound)
		},
		"malformed json": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{broken`))
		},
		"no embedding ids": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"}]}`))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			c := NewOpenAIClient(srv.URL, "k")
			models, err := c.ListModels(context.Background())
			if err != nil {
				t.Fatalf("ListModels: %v", err)
			}
			if !slices.Equal(models, staticEmbeddingModels) {
				t.Errorf("models = %v, want static fallback %v", models, staticEmbeddingModels)
			}
		})
	}

	// Transport error (nothing listening) also falls back.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := NewOpenAIClient(url, "k")
	models, err := c.ListModels(context.Background())
	if err != nil || !slices.Equal(models, staticEmbeddingModels) {
		t.Fatalf("ListModels after transport error = %v, err %v, want static fallback", models, err)
	}

	// Invalid base URL fails request construction → static fallback.
	badURL := NewOpenAIClient("://bad-url", "k")
	models, err = badURL.ListModels(context.Background())
	if err != nil || len(models) != len(staticEmbeddingModels) {
		t.Fatalf("ListModels with bad URL = %v, err %v, want static fallback", models, err)
	}
}

// --- OllamaClient -----------------------------------------------------------

func TestOllamaTrimsTrailingSlash(t *testing.T) {
	c := NewOllamaClient("http://localhost:11434/")
	if strings.HasSuffix(c.baseURL, "/") {
		t.Errorf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
}

func TestOllamaModelInfoRealDims(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("path = %q, want /api/show", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"details":{"parameter_size":"334M","family":"bert"},"model_info":{"bert.embedding_length":768,"bert.context_length":512}}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	// model_info wins even when the name would infer differently ("bge-m3"
	// infers 1024), and covers names InferDims does not know at all.
	for _, model := range []string{"bge-m3", "some-custom-model"} {
		info, err := c.ModelInfo(context.Background(), model)
		if err != nil {
			t.Fatalf("ModelInfo(%q): %v", model, err)
		}
		if info.Dims != 768 {
			t.Errorf("ModelInfo(%q).Dims = %d, want 768 (from model_info)", model, info.Dims)
		}
	}
}

func TestOllamaModelInfoFallsBackToInferDims(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"details":{"parameter_size":"334M","family":"bert"},"model_info":{}}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	cases := map[string]int{
		"nomic-embed-text":  768,
		"bge-m3":            1024,
		"mxbai-embed-large": 1024,
	}
	for model, want := range cases {
		info, err := c.ModelInfo(context.Background(), model)
		if err != nil {
			t.Fatalf("ModelInfo(%q): %v", model, err)
		}
		if info.Dims != want {
			t.Errorf("ModelInfo(%q).Dims = %d, want %d", model, info.Dims, want)
		}
	}
}

func TestOllamaModelInfoUnknownDimsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"details":{"family":"bert"},"model_info":{"bert.context_length":512}}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	// No *.embedding_length and a name InferDims does not know: no 1024 guess.
	// The typed error keeps the circuit breaker neutral (deterministic miss).
	_, err := c.ModelInfo(context.Background(), "some-other-model")
	var ume *UnknownModelError
	if !errors.As(err, &ume) {
		t.Fatalf("err = %v, want *UnknownModelError", err)
	}
	if ume.Model != "some-other-model" {
		t.Errorf("UnknownModelError.Model = %q, want some-other-model", ume.Model)
	}
}

func TestOllamaModelInfoHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewOllamaClient(srv.URL)
	if _, err := c.ModelInfo(context.Background(), "m"); err == nil || !strings.Contains(err.Error(), "ollama show failed") {
		t.Fatalf("err = %v, want ollama show failed", err)
	}
}

func TestOllamaModelInfoMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`nope`))
	}))
	defer srv.Close()
	c := NewOllamaClient(srv.URL)
	if _, err := c.ModelInfo(context.Background(), "m"); err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestOllamaModelInfoTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := NewOllamaClient(url)
	if _, err := c.ModelInfo(context.Background(), "m"); err == nil {
		t.Error("transport error should propagate")
	}
}

func TestOllamaEmbedSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %q, want /api/embed", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"model":"bge-m3","embeddings":[[0.1,0.2],[0.3,0.4]]}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	got, err := c.Embed(context.Background(), "bge-m3", "a", "b")
	if err != nil || len(got) != 2 || got[1][1] != 0.4 {
		t.Fatalf("Embed = %v, err %v", got, err)
	}
}

func TestOllamaEmbedNoInputs(t *testing.T) {
	c := NewOllamaClient("http://example.invalid")
	if _, err := c.Embed(context.Background(), "m"); err == nil {
		t.Error("Embed with no inputs should error")
	}
}

func TestOllamaEmbedHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewOllamaClient(srv.URL)
	if _, err := c.Embed(context.Background(), "m", "x"); err == nil || !strings.Contains(err.Error(), "ollama embed failed") {
		t.Fatalf("err = %v, want ollama embed failed", err)
	}
}

func TestOllamaEmbedMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{bad`))
	}))
	defer srv.Close()
	c := NewOllamaClient(srv.URL)
	if _, err := c.Embed(context.Background(), "m", "x"); err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestOllamaEmbedTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := NewOllamaClient(url)
	if _, err := c.Embed(context.Background(), "m", "x"); err == nil {
		t.Error("transport error should propagate")
	}
}

func TestOllamaSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[9,8,7]]}`))
	}))
	defer srv.Close()
	c := NewOllamaClient(srv.URL)
	vec, err := c.EmbedSingle(context.Background(), "m", "hi")
	if err != nil || len(vec) != 3 || vec[0] != 9 {
		t.Fatalf("EmbedSingle = %v, err %v", vec, err)
	}
}

func TestOllamaSingleEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[]}`))
	}))
	defer srv.Close()
	c := NewOllamaClient(srv.URL)
	if _, err := c.EmbedSingle(context.Background(), "m", "hi"); err == nil {
		t.Error("empty embeddings should yield 'no embedding returned'")
	}
}

func TestOllamaListModelsFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("path = %q, want /api/tags", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[
			{"name":"nomic-embed-text:latest"},
			{"name":"bge-m3:latest"},
			{"name":"llama3:8b"},
			{"name":"mxbai-embed-large"}
		]}`))
	}))
	defer srv.Close()

	c := NewOllamaClient(srv.URL)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	// Only embedding models are kept; llama3 is filtered out.
	if len(models) != 3 {
		t.Fatalf("models = %v, want 3 embedding models", models)
	}
	for _, m := range models {
		if strings.Contains(m, "llama") {
			t.Errorf("non-embedding model leaked: %q", m)
		}
	}
}

func TestOllamaListModelsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()
	c := NewOllamaClient(srv.URL)
	if _, err := c.ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "ollama tags failed") {
		t.Fatalf("err = %v, want ollama tags failed", err)
	}
}

func TestOllamaListModelsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{oops`))
	}))
	defer srv.Close()
	c := NewOllamaClient(srv.URL)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Error("malformed JSON should error")
	}
}

func TestOllamaListModelsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := NewOllamaClient(url)
	if _, err := c.ListModels(context.Background()); err == nil {
		t.Error("transport error should propagate")
	}
}

// --- ChainEmbedder (Embed / ModelInfo / SetPrivacy / ListModels edges) ------

func TestChainEmbedBatchFallback(t *testing.T) {
	var calls []string
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "remote", Local: false, Embedder: &fakeEmbedder{name: "remote", fail: true, calls: &calls}},
		{Name: "local", Local: true, Embedder: &fakeEmbedder{name: "local", single: []float32{1, 2, 3}, calls: &calls}},
	}, false)

	got, err := chain.Embed(context.Background(), "m", "a")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("Embed = %v", got)
	}
	if len(calls) != 2 || calls[0] != "remote" || calls[1] != "local" {
		t.Errorf("call order = %v, want [remote local]", calls)
	}
}

func TestChainEmbedAllFail(t *testing.T) {
	var calls []string
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "a", Local: true, Embedder: &fakeEmbedder{name: "a", fail: true, calls: &calls}},
	}, false)
	if _, err := chain.Embed(context.Background(), "m", "x"); err == nil {
		t.Error("Embed should error when all providers fail")
	}
}

func TestChainModelInfo(t *testing.T) {
	var calls []string
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "remote", Local: false, Embedder: &fakeEmbedder{name: "remote", fail: true, calls: &calls}},
		{Name: "local", Local: true, Embedder: &fakeEmbedder{name: "local", calls: &calls}},
	}, false)

	info, err := chain.ModelInfo(context.Background(), "m")
	if err != nil || info.Dims != 3 {
		t.Fatalf("ModelInfo = %+v, err %v", info, err)
	}
	if len(calls) != 2 {
		t.Errorf("calls = %v, want both tried", calls)
	}

	// All fail → error.
	chain2 := NewChainEmbedder([]ProviderInstance{
		{Name: "a", Local: true, Embedder: &fakeEmbedder{name: "a", fail: true, calls: &calls}},
	}, false)
	if _, err := chain2.ModelInfo(context.Background(), "m"); err == nil {
		t.Error("ModelInfo should error when all providers fail")
	}
}

func TestChainSetPrivacyTogglesRemote(t *testing.T) {
	var calls []string
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "remote", Local: false, Embedder: &fakeEmbedder{name: "remote", single: []float32{1}, calls: &calls}},
		{Name: "local", Local: true, Embedder: &fakeEmbedder{name: "local", single: []float32{2}, calls: &calls}},
	}, false)

	// Privacy off: remote is tried first.
	if _, err := chain.Embed(context.Background(), "m", "x"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if calls[0] != "remote" {
		t.Errorf("privacy-off first call = %q, want remote", calls[0])
	}

	// Turn privacy on: remote is now skipped.
	calls = nil
	chain.SetPrivacy(true)
	if _, err := chain.Embed(context.Background(), "m", "x"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(calls) != 1 || calls[0] != "local" {
		t.Errorf("privacy-on calls = %v, want [local]", calls)
	}
}

func TestChainListModelsNoneAvailable(t *testing.T) {
	var calls []string
	// A single remote provider, privacy on → skipped → no models → error.
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "remote", Local: false, Embedder: &fakeEmbedder{name: "remote", calls: &calls}},
	}, true)
	if _, err := chain.ListModels(context.Background()); err == nil {
		t.Error("ListModels should error when no provider is available")
	}
}

func TestChainStopsOnCancelledContext(t *testing.T) {
	var calls []string
	// A failing provider under a cancelled context: the chain must abandon the
	// fallback loop and return the context error rather than trying more providers.
	mkChain := func() *ChainEmbedder {
		calls = nil
		return NewChainEmbedder([]ProviderInstance{
			{Name: "a", Local: true, Embedder: &fakeEmbedder{name: "a", fail: true, calls: &calls}},
			{Name: "b", Local: true, Embedder: &fakeEmbedder{name: "b", fail: true, calls: &calls}},
		}, false)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := mkChain().Embed(ctx, "m", "x"); err != context.Canceled {
		t.Errorf("Embed on cancelled ctx = %v, want context.Canceled", err)
	}
	if len(calls) != 1 {
		t.Errorf("Embed tried %d providers, want 1 (bail on cancel)", len(calls))
	}
	if _, err := mkChain().EmbedSingle(ctx, "m", "x"); err != context.Canceled {
		t.Errorf("EmbedSingle on cancelled ctx = %v, want context.Canceled", err)
	}
	if _, err := mkChain().ModelInfo(ctx, "m"); err != context.Canceled {
		t.Errorf("ModelInfo on cancelled ctx = %v, want context.Canceled", err)
	}
}

func TestChainListModelsSkipsFailingProvider(t *testing.T) {
	var calls []string
	// First provider errors on ListModels (continue), second returns a model.
	chain := NewChainEmbedder([]ProviderInstance{
		{Name: "bad", Local: true, Embedder: &fakeEmbedder{name: "bad", fail: true, calls: &calls}},
		{Name: "good", Local: true, Embedder: &fakeEmbedder{name: "good", calls: &calls}},
	}, false)
	models, err := chain.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 || models[0] != "good-model" {
		t.Errorf("models = %v, want [good-model]", models)
	}
}

// --- embedTimeout -----------------------------------------------------------

func TestEmbedTimeout(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"unset uses default", "", defaultEmbedTimeout},
		{"duration string", "45s", 45 * time.Second},
		{"bare seconds", "120", 120 * time.Second},
		{"invalid falls back", "nonsense", defaultEmbedTimeout},
		{"zero falls back", "0", defaultEmbedTimeout},
		{"negative falls back", "-5s", defaultEmbedTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SEMIDX_EMBED_TIMEOUT", tc.env)
			if got := embedTimeout(); got != tc.want {
				t.Errorf("embedTimeout() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewOllamaClientUsesConfiguredTimeout(t *testing.T) {
	t.Setenv("SEMIDX_EMBED_TIMEOUT", "50s")
	c := NewOllamaClient("http://x")
	if c.client.Timeout != 50*time.Second {
		t.Errorf("client timeout = %v, want 50s", c.client.Timeout)
	}
}
