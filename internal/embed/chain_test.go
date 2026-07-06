package embed

import (
	"testing"
)

func TestNewChainFromConfigSingleOllama(t *testing.T) {
	cfg := ChainConfig{OllamaURL: "http://localhost:11434"}
	emb := NewChainFromConfig(cfg)
	if emb == nil {
		t.Fatal("NewChainFromConfig returned nil")
	}
	// A single Ollama URL yields a ChainEmbedder, not a ParallelEmbedder.
	if _, ok := emb.(*ChainEmbedder); !ok {
		t.Errorf("expected *ChainEmbedder, got %T", emb)
	}
}

func TestNewChainFromConfigWithGemini(t *testing.T) {
	cfg := ChainConfig{
		OllamaURL:     "http://localhost:11434",
		GeminiAPIKey:  "test-key",
		GeminiBaseURL: "http://localhost:9999/v1",
	}
	emb := NewChainFromConfig(cfg)
	if emb == nil {
		t.Fatal("NewChainFromConfig returned nil")
	}
	// Gemini + Ollama still composes into a ChainEmbedder.
	if _, ok := emb.(*ChainEmbedder); !ok {
		t.Errorf("expected *ChainEmbedder, got %T", emb)
	}
}

func TestNewChainFromConfigPoolWithTwoOllamas(t *testing.T) {
	cfg := ChainConfig{
		OllamaURLs: []string{"http://localhost:11434", "http://localhost:11434"},
	}
	emb := NewChainFromConfig(cfg)
	if emb == nil {
		t.Fatal("NewChainFromConfig returned nil")
	}
	// Should return a ParallelEmbedder since 2+ Ollama URLs.
	_, ok := emb.(*ParallelEmbedder)
	if !ok {
		t.Errorf("expected *ParallelEmbedder, got %T", emb)
	}
}

func TestNewChainFromConfigPoolWithCloudProviders(t *testing.T) {
	cfg := ChainConfig{
		OllamaURLs:         []string{"http://localhost:11434", "http://localhost:11434"},
		GeminiAPIKey:       "gk",
		GeminiBaseURL:      "http://g:1/v1",
		GroqAPIKey:         "grk",
		GroqBaseURL:        "http://gq:1/v1",
		OpenRouterAPIKey:   "ork",
		OpenRouterBaseURL:  "http://or:1/v1",
		OllamaCloudAPIKey:  "ock",
		OllamaCloudBaseURL: "http://oc:1/v1",
	}
	emb := NewChainFromConfig(cfg)
	if emb == nil {
		t.Fatal("NewChainFromConfig returned nil")
	}
	// Should still be a ParallelEmbedder (with cloud chain as extra entry).
	_, ok := emb.(*ParallelEmbedder)
	if !ok {
		t.Errorf("expected *ParallelEmbedder, got %T", emb)
	}
}
