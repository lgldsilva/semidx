package llm

import (
	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openaicompat"
	"charm.land/fantasy/providers/openrouter"
)

func buildAnthropicProvider(cfg ProviderConfig) (fantasy.Provider, error) {
	opts := []anthropic.Option{anthropic.WithName(string(cfg.Type))}
	if cfg.APIKey != "" {
		opts = append(opts, anthropic.WithAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(cfg.BaseURL))
	}
	return anthropic.New(opts...)
}

func buildGoogleProvider(cfg ProviderConfig) (fantasy.Provider, error) {
	opts := []google.Option{google.WithName(string(cfg.Type))}
	if cfg.APIKey != "" {
		opts = append(opts, google.WithGeminiAPIKey(cfg.APIKey))
	}
	if cfg.BaseURL != "" {
		opts = append(opts, google.WithBaseURL(cfg.BaseURL))
	}
	return google.New(opts...)
}

func buildOpenRouterProvider(cfg ProviderConfig) (fantasy.Provider, error) {
	opts := []openrouter.Option{openrouter.WithName(string(cfg.Type))}
	if cfg.APIKey != "" {
		opts = append(opts, openrouter.WithAPIKey(cfg.APIKey))
	}
	return openrouter.New(opts...)
}

func buildOpenAICompatProvider(cfg ProviderConfig, defaultBase string, requireBase bool) (fantasy.Provider, error) {
	base := cfg.BaseURL
	if base == "" {
		base = defaultBase
	}
	if requireBase && base == "" {
		return nil, fmt.Errorf("llm: %s provider requires BaseURL", cfg.Type)
	}
	opts := []openaicompat.Option{
		openaicompat.WithName(string(cfg.Type)),
		openaicompat.WithBaseURL(base),
	}
	if cfg.APIKey != "" {
		opts = append(opts, openaicompat.WithAPIKey(cfg.APIKey))
	}
	return openaicompat.New(opts...)
}

func buildCopilotProvider(cfg ProviderConfig) (fantasy.Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("llm: %s provider requires a GitHub token (SEMIDX_COPILOT_TOKEN or SEMIDX_GITHUB_TOKEN)", cfg.Type)
	}
	base := cfg.BaseURL
	if base == "" {
		base = copilotDefaultAPIBase
	}
	opts := []openaicompat.Option{
		openaicompat.WithName(string(cfg.Type)),
		openaicompat.WithBaseURL(base),
		openaicompat.WithAPIKey("copilot"),
		openaicompat.WithHTTPClient(newCopilotDoer(cfg.APIKey, "", nil)),
	}
	return openaicompat.New(opts...)
}
