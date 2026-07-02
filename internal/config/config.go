// Package config resolves all runtime configuration for semidx.
//
// Precedence, highest first:
//  1. real environment variables
//  2. a .env file in the current working directory
//  3. built-in defaults
//
// The .env parser intentionally mirrors the PoC's legacy behavior: one
// KEY=VALUE per line, blank lines and "#" comments skipped, no quote
// stripping, and values never override variables already exported in the
// real environment.
package config

import (
	"os"
	"strings"
)

const (
	defaultDatabaseURL = "postgres://semantic:semantic@localhost:55432/semantic_indexer"
	defaultOllamaURL   = "http://localhost:11434"
)

// Config holds every runtime setting the CLI and MCP server need.
type Config struct {
	// DatabaseURL is the PostgreSQL/pgvector DSN (SEMIDX_DB_DSN).
	DatabaseURL string
	// OllamaURL is the local Ollama endpoint (SEMIDX_OLLAMA_URL, legacy OLLAMA_URL).
	OllamaURL string

	// Provider optionally prepends a custom provider to the embedding chain
	// (EMBED_PROVIDER: "openai" or "ollama"), served at Endpoint with APIKey.
	Provider string
	Endpoint string
	APIKey   string

	GeminiAPIKey      string
	GroqAPIKey        string
	OpenRouterAPIKey  string
	OllamaCloudAPIKey string

	// Privacy forces local-only embedding providers (EMBED_PRIVACY=true).
	Privacy bool
}

// Load resolves the configuration. A missing or unreadable .env file is not
// an error; malformed lines in it are skipped.
func Load() *Config {
	env := newResolver(".env")
	return &Config{
		DatabaseURL:       env.get("SEMIDX_DB_DSN", defaultDatabaseURL),
		OllamaURL:         env.first("SEMIDX_OLLAMA_URL", "OLLAMA_URL", defaultOllamaURL),
		Provider:          env.get("EMBED_PROVIDER", ""),
		Endpoint:          env.get("EMBED_ENDPOINT", ""),
		APIKey:            env.get("EMBED_API_KEY", ""),
		GeminiAPIKey:      env.get("GEMINI_API_KEY", ""),
		GroqAPIKey:        env.get("GROQ_API_KEY", ""),
		OpenRouterAPIKey:  env.get("OPENROUTER_API_KEY", ""),
		OllamaCloudAPIKey: env.get("OLLAMA_CLOUD_API_KEY", ""),
		Privacy:           env.get("EMBED_PRIVACY", "") == "true",
	}
}

// resolver looks a key up in the real environment first, then in the parsed
// .env file, then falls back to the given default. Empty values count as
// unset at every level.
type resolver struct {
	fileVars map[string]string
}

func newResolver(path string) *resolver {
	vars := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		return &resolver{fileVars: vars}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		vars[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return &resolver{fileVars: vars}
}

func (r *resolver) get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v := r.fileVars[key]; v != "" {
		return v
	}
	return def
}

// first returns the value of the first key that resolves at the highest
// available level, checking all keys against the real environment before
// falling back to the .env file.
func (r *resolver) first(primary, legacy, def string) string {
	if v := os.Getenv(primary); v != "" {
		return v
	}
	if v := os.Getenv(legacy); v != "" {
		return v
	}
	if v := r.fileVars[primary]; v != "" {
		return v
	}
	if v := r.fileVars[legacy]; v != "" {
		return v
	}
	return def
}
