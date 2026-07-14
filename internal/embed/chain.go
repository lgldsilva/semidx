// Package embed — see embed.go for overview.
package embed

import "time"

// ChainConfig holds the provider settings needed to build an embedder chain.
type ChainConfig struct {
	// OllamaURL is the default local Ollama endpoint.
	OllamaURL string
	// OllamaURLs, when 2+, enables parallel-pool mode.
	OllamaURLs []string

	// Custom provider override (EMBED_PROVIDER / --provider).
	Provider string
	Endpoint string
	APIKey   string

	// Cloud providers.
	GeminiAPIKey       string
	GeminiBaseURL      string
	GroqAPIKey         string
	GroqBaseURL        string
	OpenRouterAPIKey   string
	OpenRouterBaseURL  string
	OllamaCloudAPIKey  string
	OllamaCloudBaseURL string

	// Privacy forces local-only embedding providers.
	Privacy bool

	// CircuitThreshold is the consecutive failure count before the circuit
	// breaker opens for a provider. Zero means the default (3).
	CircuitThreshold int
	// CircuitCooldown is the duration the circuit stays open before allowing
	// a probe request. Zero means the default (30s).
	CircuitCooldown time.Duration
}

// NewChainFromConfig builds the embedding chain from the application config.
// It reads provider URLs, API keys, and privacy settings to construct the
// appropriate embedder chain (Ollama → Groq → OpenRouter → …). When 2 or more
// Ollama URLs are configured it returns a ParallelEmbedder for round-robin
// across Ollama instances with cloud providers as an additional lane.
//
// Client-side rate limiting is deliberately NOT implemented here: the
// per-provider circuit breaker (which a 429 response feeds), the provider's
// own retry semantics, and the chain's fallback to the next provider already
// bound request pressure. A local limiter would add latency and a second,
// drifting source of truth for each provider's quota.
func NewChainFromConfig(cfg ChainConfig) Embedder {
	// Pool mode: when 2+ Ollama URLs are configured, create a ParallelEmbedder
	// with one entry per Ollama instance plus one entry bundling all cloud
	// providers as a fallback chain. This distributes requests across Ollama
	// instances while cloud providers serve as an additional parallel lane.
	if len(cfg.OllamaURLs) >= 2 {
		return buildPool(cfg)
	}

	// Single-URL or no explicit URLs: current chain behaviour (backward compatible).
	var providers []ProviderInstance

	wrap := func(name string, inner Embedder, local bool) ProviderInstance {
		return ProviderInstance{
			Name:     name,
			Embedder: wrapWithCircuit(name, inner, cfg.CircuitThreshold, cfg.CircuitCooldown),
			Local:    local,
		}
	}

	if cfg.GeminiAPIKey != "" {
		providers = append(providers, wrap("gemini",
			NewOpenAIClient(cfg.GeminiBaseURL, cfg.GeminiAPIKey), false))
	}
	if cfg.GroqAPIKey != "" {
		providers = append(providers, wrap("groq",
			NewOpenAIClient(cfg.GroqBaseURL, cfg.GroqAPIKey), false))
	}
	if cfg.OpenRouterAPIKey != "" {
		providers = append(providers, wrap("openrouter",
			NewOpenAIClient(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey), false))
	}
	if cfg.OllamaCloudAPIKey != "" {
		providers = append(providers, wrap("ollama-cloud",
			NewOpenAIClient(cfg.OllamaCloudBaseURL, cfg.OllamaCloudAPIKey), false))
	}

	// Local Ollama is always the final fallback.
	providers = append(providers, wrap("ollama",
		NewOllamaClient(cfg.OllamaURL), true))

	// A custom EMBED_PROVIDER override is prepended ahead of the defaults.
	if cfg.Provider != "" {
		endpoint := cfg.Endpoint
		if endpoint == "" {
			if cfg.Provider == "ollama" {
				endpoint = cfg.OllamaURL
			} else {
				endpoint = "https://api.openai.com/v1"
			}
		}
		var custom Embedder
		if cfg.Provider == "openai" {
			custom = NewOpenAIClient(endpoint, cfg.APIKey)
		} else {
			custom = NewOllamaClient(endpoint)
		}
		providers = append([]ProviderInstance{wrap("custom", custom, cfg.Provider == "ollama")}, providers...)
	}

	return NewChainEmbedder(providers, cfg.Privacy)
}

// buildPool creates a ParallelEmbedder when multiple Ollama URLs are configured.
// Each Ollama URL becomes an independent pool entry (local round-robin). Cloud
// providers (Gemini, Groq, OpenRouter, OllamaCloud) are bundled as one
// ChainEmbedder entry so they can still fall through to each other.
func buildPool(cfg ChainConfig) Embedder {
	var pool []Embedder

	// One entry per Ollama URL (local or remote).
	for _, url := range cfg.OllamaURLs {
		pool = append(pool,
			wrapWithCircuit(url, NewOllamaClient(url), cfg.CircuitThreshold, cfg.CircuitCooldown))
	}

	// Cloud providers bundled as one fallback-chain entry.
	if cloud := buildCloudChain(cfg); cloud != nil {
		pool = append(pool,
			wrapWithCircuit("cloud", cloud, cfg.CircuitThreshold, cfg.CircuitCooldown))
	}

	// Custom provider, if configured, as its own pool entry.
	if custom := customPoolEntry(cfg); custom != nil {
		pool = append(pool,
			wrapWithCircuit("custom", custom, cfg.CircuitThreshold, cfg.CircuitCooldown))
	}

	return NewParallelEmbedder(pool)
}

// buildCloudChain bundles the configured cloud providers (Gemini, Groq,
// OpenRouter, OllamaCloud) into a single fallback-chain embedder so they can
// fall through to each other. Returns nil when no cloud provider is configured.
func buildCloudChain(cfg ChainConfig) Embedder {
	wrap := func(name string, inner Embedder) ProviderInstance {
		return ProviderInstance{
			Name:     name,
			Embedder: wrapWithCircuit(name, inner, cfg.CircuitThreshold, cfg.CircuitCooldown),
			Local:    false,
		}
	}
	var cloud []ProviderInstance
	if cfg.GeminiAPIKey != "" {
		cloud = append(cloud, wrap("gemini", NewOpenAIClient(cfg.GeminiBaseURL, cfg.GeminiAPIKey)))
	}
	if cfg.GroqAPIKey != "" {
		cloud = append(cloud, wrap("groq", NewOpenAIClient(cfg.GroqBaseURL, cfg.GroqAPIKey)))
	}
	if cfg.OpenRouterAPIKey != "" {
		cloud = append(cloud, wrap("openrouter", NewOpenAIClient(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey)))
	}
	if cfg.OllamaCloudAPIKey != "" {
		cloud = append(cloud, wrap("ollama-cloud", NewOpenAIClient(cfg.OllamaCloudBaseURL, cfg.OllamaCloudAPIKey)))
	}
	if len(cloud) == 0 {
		return nil
	}
	return NewChainEmbedder(cloud, cfg.Privacy)
}

// customPoolEntry builds the optional custom-provider pool entry from cfg, or
// returns nil when no custom provider is configured.
func customPoolEntry(cfg ChainConfig) Embedder {
	if cfg.Provider == "" {
		return nil
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		if cfg.Provider == "ollama" {
			endpoint = cfg.OllamaURLs[0] // use first Ollama URL for custom ollama
		} else {
			endpoint = "https://api.openai.com/v1"
		}
	}
	if cfg.Provider == "openai" {
		return NewOpenAIClient(endpoint, cfg.APIKey)
	}
	return NewOllamaClient(endpoint)
}
