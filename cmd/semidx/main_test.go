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
	// Should return a ChainEmbedder (not ParallelEmbedder) since only 1 Ollama.
	info, err := emb.ModelInfo(t.Context(), "bge-m3")
	if err != nil {
		t.Fatal(err)
	}
	if info.Dims == 0 {
		t.Error("expected non-zero dims from Ollama ModelInfo")
	}
}

func TestBuildChainWithGemini(t *testing.T) {
	cfg := &config.Config{
		OllamaURL:    "http://localhost:11434",
		GeminiAPIKey: "test-key",
		GeminiBaseURL: "http://localhost:9999/v1",
	}
	emb := buildChain(cfg)
	if emb == nil {
		t.Fatal("buildChain returned nil")
	}
	// ChainEmbedder with gemini + ollama
	info, err := emb.ModelInfo(t.Context(), "bge-m3")
	if err != nil {
		t.Fatal(err)
	}
	if info.Dims == 0 {
		t.Error("expected non-zero dims")
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
		OllamaURLs:        []string{"http://localhost:11434", "http://localhost:11434"},
		GeminiAPIKey:      "gk",
		GeminiBaseURL:     "http://g:1/v1",
		GroqAPIKey:        "grk",
		GroqBaseURL:       "http://gq:1/v1",
		OpenRouterAPIKey:  "ork",
		OpenRouterBaseURL: "http://or:1/v1",
		OllamaCloudAPIKey: "ock",
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
