package embed

import (
	"context"
	"testing"
)

// BenchmarkNewChainFromConfig_SingleOllama benchmarks constructing the chain
// with the simplest config (single Ollama URL → ChainEmbedder).
func BenchmarkNewChainFromConfig_SingleOllama(b *testing.B) {
	b.ReportAllocs()
	cfg := ChainConfig{OllamaURL: "http://localhost:11434"}
	b.ResetTimer()
	for b.Loop() {
		NewChainFromConfig(cfg)
	}
}

// BenchmarkNewChainFromConfig_FullChain benchmarks constructing a chain with
// every cloud provider configured, so all ProviderInstance entries are built.
func BenchmarkNewChainFromConfig_FullChain(b *testing.B) {
	b.ReportAllocs()
	cfg := ChainConfig{
		OllamaURL:          "http://localhost:11434",
		GeminiAPIKey:       "test-gemini-key",
		GeminiBaseURL:      "http://gemini:1/v1",
		GroqAPIKey:         "test-groq-key",
		GroqBaseURL:        "http://groq:1/v1",
		OpenRouterAPIKey:   "test-or-key",
		OpenRouterBaseURL:  "http://openrouter:1/v1",
		OllamaCloudAPIKey:  "test-oc-key",
		OllamaCloudBaseURL: "http://ollama-cloud:1/v1",
		Privacy:            false,
	}
	b.ResetTimer()
	for b.Loop() {
		NewChainFromConfig(cfg)
	}
}

// BenchmarkNewChainFromConfig_Pool benchmarks constructing a parallel pool
// from 3 Ollama URLs.
func BenchmarkNewChainFromConfig_Pool(b *testing.B) {
	b.ReportAllocs()
	cfg := ChainConfig{
		OllamaURLs: []string{
			"http://ollama-1:11434",
			"http://ollama-2:11434",
			"http://ollama-3:11434",
		},
	}
	b.ResetTimer()
	for b.Loop() {
		NewChainFromConfig(cfg)
	}
}

// BenchmarkChainEmbedder_Dispatch measures the chain dispatch overhead when
// the first provider succeeds (best case). Uses fake embedders wrapped with
// circuit breakers so no network calls are made.
func BenchmarkChainEmbedder_Dispatch_Success(b *testing.B) {
	b.ReportAllocs()
	chain := NewChainEmbedder([]ProviderInstance{
		{
			Name:     "remote-a",
			Embedder: wrapWithCircuit("remote-a", &okEmbedder{name: "a"}, 3, 0),
			Local:    false,
		},
		{
			Name:     "local",
			Embedder: wrapWithCircuit("local", &okEmbedder{name: "b"}, 3, 0),
			Local:    true,
		},
	}, false)

	ctx := context.Background()
	inputs := make([]string, 20)
	for i := range inputs {
		inputs[i] = "benchmark input chunk content for embedding"
	}
	b.ResetTimer()
	for b.Loop() {
		_, _ = chain.Embed(ctx, "test", inputs...)
	}
}

// BenchmarkChainEmbedder_Dispatch_Fallback measures the chain dispatch
// overhead when the first provider fails and the second succeeds.
func BenchmarkChainEmbedder_Dispatch_Fallback(b *testing.B) {
	b.ReportAllocs()
	chain := NewChainEmbedder([]ProviderInstance{
		{
			Name:     "failing",
			Embedder: wrapWithCircuit("failing", &errEmbedder{}, 1, 0),
			Local:    false,
		},
		{
			Name:     "local",
			Embedder: wrapWithCircuit("local", &okEmbedder{name: "ok"}, 3, 0),
			Local:    true,
		},
	}, false)

	ctx := context.Background()
	inputs := make([]string, 10)
	for i := range inputs {
		inputs[i] = "benchmark input chunk content for embedding"
	}
	b.ResetTimer()
	for b.Loop() {
		_, _ = chain.Embed(ctx, "test", inputs...)
	}
}

// BenchmarkChainEmbedder_EmbedSingle measures EmbedSingle dispatch overhead
// (best case).
func BenchmarkChainEmbedder_EmbedSingle(b *testing.B) {
	b.ReportAllocs()
	chain := NewChainEmbedder([]ProviderInstance{
		{
			Name:     "local",
			Embedder: wrapWithCircuit("local", &okEmbedder{name: "c"}, 3, 0),
			Local:    true,
		},
	}, false)

	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		_, _ = chain.EmbedSingle(ctx, "test", "query text")
	}
}
