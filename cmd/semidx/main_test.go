package main

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
)

func TestBuildChainSingleOllama(t *testing.T) {
	cfg := &config.Config{OllamaURL: "http://localhost:11434"}
	emb := buildChain(cfg)
	if emb == nil {
		t.Fatal("buildChain returned nil")
	}
	// A single Ollama URL yields a ChainEmbedder, not a ParallelEmbedder.
	// Assert the wiring without a live provider (ModelInfo would hit the network).
	if _, ok := emb.(*embed.ChainEmbedder); !ok {
		t.Errorf("expected *ChainEmbedder, got %T", emb)
	}
}

func TestBuildChainWithGemini(t *testing.T) {
	cfg := &config.Config{
		OllamaURL:     "http://localhost:11434",
		GeminiAPIKey:  "test-key",
		GeminiBaseURL: "http://localhost:9999/v1",
	}
	emb := buildChain(cfg)
	if emb == nil {
		t.Fatal("buildChain returned nil")
	}
	// Gemini + Ollama still composes into a ChainEmbedder. Assert the type
	// rather than calling ModelInfo, which would require live providers.
	if _, ok := emb.(*embed.ChainEmbedder); !ok {
		t.Errorf("expected *ChainEmbedder, got %T", emb)
	}
}

func TestBuildPoolWithTwoOllamas(t *testing.T) {
	cfg := &config.Config{
		OllamaURLs: []string{"http://localhost:11434", "http://localhost:11434"},
	}
	emb := buildChain(cfg)
	if emb == nil {
		t.Fatal("buildChain returned nil")
	}
	// Should return a ParallelEmbedder since 2+ Ollama URLs.
	_, ok := emb.(*embed.ParallelEmbedder)
	if !ok {
		t.Errorf("expected *ParallelEmbedder, got %T", emb)
	}
}

func TestBuildPoolWithCloudProviders(t *testing.T) {
	cfg := &config.Config{
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
	emb := buildChain(cfg)
	if emb == nil {
		t.Fatal("buildChain returned nil")
	}
	// Should still be a ParallelEmbedder (with cloud chain as extra entry).
	_, ok := emb.(*embed.ParallelEmbedder)
	if !ok {
		t.Errorf("expected *ParallelEmbedder, got %T", emb)
	}
}

func TestSystemDirsBlocked(t *testing.T) {
	if !systemDirs["/"] {
		t.Error("/ should be blocked")
	}
	if !systemDirs["/etc"] {
		t.Error("/etc should be blocked")
	}
	if systemDirs["/home/user/project"] {
		t.Error("/home/user/project should NOT be blocked")
	}
}

func TestDocsFlagHint(t *testing.T) {
	if docsFlagHint(true) != " --docs" {
		t.Errorf("docsFlagHint(true) = %q, want ' --docs'", docsFlagHint(true))
	}
	if docsFlagHint(false) != "" {
		t.Errorf("docsFlagHint(false) = %q, want ''", docsFlagHint(false))
	}
}
