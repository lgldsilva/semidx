// Package llm builds and resolves chat LLM providers/models on top of
// charm.land/fantasy. It replaces the hand-rolled OpenAI-compatible clients in
// internal/chat with fantasy's multi-vendor provider layer (native Anthropic,
// Google, OpenRouter, plus any OpenAI-compatible endpoint such as Groq), which
// brings tool-calling normalization, streaming aggregation, centralized retry
// and JSON repair for free.
//
// Scope: this package covers chat/tool-calling only. Embeddings, privacy
// routing and RAG stay in their own packages (internal/embed, internal/privacy,
// internal/search) — fantasy does not touch them.
package llm

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openaicompat"
	"charm.land/fantasy/providers/openrouter"
)

// ProviderType identifies a chat LLM backend.
type ProviderType string

const (
	ProviderGoogle     ProviderType = "google"
	ProviderAnthropic  ProviderType = "anthropic"
	ProviderOpenRouter ProviderType = "openrouter"
	ProviderGroq       ProviderType = "groq"
)

// groqDefaultBaseURL is Groq's OpenAI-compatible endpoint. Groq has no native
// fantasy provider, so it is reached through the openaicompat adapter.
const groqDefaultBaseURL = "https://api.groq.com/openai/v1"

// ProviderConfig describes how to build one provider. APIKey and BaseURL come
// from the semidx config (internal/config); BaseURL is an optional override
// (empty = the provider's default endpoint).
type ProviderConfig struct {
	Type    ProviderType
	APIKey  string
	BaseURL string
}

// BuildProvider constructs a fantasy.Provider for cfg. Each vendor has its own
// Option type, so the cases cannot share an options slice.
func BuildProvider(cfg ProviderConfig) (fantasy.Provider, error) {
	switch cfg.Type {
	case ProviderAnthropic:
		opts := []anthropic.Option{anthropic.WithName(string(cfg.Type))}
		if cfg.APIKey != "" {
			opts = append(opts, anthropic.WithAPIKey(cfg.APIKey))
		}
		if cfg.BaseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(cfg.BaseURL))
		}
		return anthropic.New(opts...)

	case ProviderGoogle:
		opts := []google.Option{google.WithName(string(cfg.Type))}
		if cfg.APIKey != "" {
			opts = append(opts, google.WithGeminiAPIKey(cfg.APIKey))
		}
		if cfg.BaseURL != "" {
			opts = append(opts, google.WithBaseURL(cfg.BaseURL))
		}
		return google.New(opts...)

	case ProviderOpenRouter:
		opts := []openrouter.Option{openrouter.WithName(string(cfg.Type))}
		if cfg.APIKey != "" {
			opts = append(opts, openrouter.WithAPIKey(cfg.APIKey))
		}
		return openrouter.New(opts...)

	case ProviderGroq:
		base := cfg.BaseURL
		if base == "" {
			base = groqDefaultBaseURL
		}
		opts := []openaicompat.Option{
			openaicompat.WithName(string(cfg.Type)),
			openaicompat.WithBaseURL(base),
		}
		if cfg.APIKey != "" {
			opts = append(opts, openaicompat.WithAPIKey(cfg.APIKey))
		}
		return openaicompat.New(opts...)

	default:
		return nil, fmt.Errorf("llm: unknown provider type %q", cfg.Type)
	}
}

// ResolveModel builds the provider for cfg and resolves a language model by ID
// (e.g. "claude-sonnet-4-latest", "gemini-2.5-flash"). It is the single entry
// point the agent/RAG layers use to obtain a fantasy.LanguageModel.
func ResolveModel(ctx context.Context, cfg ProviderConfig, modelID string) (fantasy.LanguageModel, error) {
	p, err := BuildProvider(cfg)
	if err != nil {
		return nil, err
	}
	m, err := p.LanguageModel(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("llm: resolve model %q on %s: %w", modelID, cfg.Type, err)
	}
	return m, nil
}
