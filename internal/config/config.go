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
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultDatabaseURL  = "postgres://semantic:semantic@localhost:55432/semantic_indexer"
	defaultOllamaURL    = "http://localhost:11434"
	defaultIndexWorkers = 4
	defaultListenAddr   = ":8080"
	defaultDataDir      = "/var/lib/semidx"
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

	// IndexWorkers is how many files are indexed concurrently
	// (SEMIDX_INDEX_WORKERS). Defaults to defaultIndexWorkers.
	IndexWorkers int

	// ListenAddr is the server bind address (SEMIDX_LISTEN_ADDR, e.g. ":8080").
	ListenAddr string
	// BootstrapToken optionally sets the first admin token on an empty server
	// (SEMIDX_BOOTSTRAP_TOKEN); if empty, one is generated and logged once.
	BootstrapToken string
	// DataDir is where the server clones git projects (SEMIDX_DATA_DIR).
	DataDir string
	// BootstrapAdminUser/Password create the first web-UI admin on an empty
	// server (SEMIDX_BOOTSTRAP_ADMIN_USER, default "admin";
	// SEMIDX_BOOTSTRAP_ADMIN_PASSWORD). No user is created when the password is
	// empty.
	BootstrapAdminUser     string
	BootstrapAdminPassword string
	// CookieSecure sets the Secure flag on web-admin cookies
	// (SEMIDX_COOKIE_SECURE, default true). Set to false ONLY when serving the
	// admin UI over plain HTTP (e.g. local testing).
	CookieSecure bool
	// JWTSecret is the HS256 signing key for control tokens
	// (SEMIDX_JWT_SECRET). When empty, JWT control tokens are disabled.
	JWTSecret string
	// LocalIndexPath, when non-empty, makes the CLI index and search a local
	// SQLite file instead of PostgreSQL (SEMIDX_LOCAL_INDEX: a path, or a truthy
	// value to use the default location). Empty means server/Postgres mode.
	LocalIndexPath string
	// KeywordOnly indexes and searches without any embedding model — text is
	// stored and matched by keyword (SEMIDX_EMBED_MODE=none). The zero-dependency
	// baseline for a machine with no GPU, API key or Ollama.
	KeywordOnly bool
}

// DefaultLocalIndexPath is the standalone index location, honoring XDG_DATA_HOME.
func DefaultLocalIndexPath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "semidx-index.db"
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "semidx", "index.db")
}

// resolveLocalIndex maps SEMIDX_LOCAL_INDEX to a path: empty → "" (server mode);
// a truthy flag → the default path; anything else → that literal path.
func resolveLocalIndex(v string) string {
	switch v {
	case "":
		return ""
	case "1", "true", "yes", "on":
		return DefaultLocalIndexPath()
	default:
		return v
	}
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
		IndexWorkers:      atoiDefault(env.get("SEMIDX_INDEX_WORKERS", ""), defaultIndexWorkers),
		ListenAddr:        env.get("SEMIDX_LISTEN_ADDR", defaultListenAddr),
		BootstrapToken:    env.get("SEMIDX_BOOTSTRAP_TOKEN", ""),
		DataDir:           env.get("SEMIDX_DATA_DIR", defaultDataDir),

		BootstrapAdminUser:     env.get("SEMIDX_BOOTSTRAP_ADMIN_USER", "admin"),
		BootstrapAdminPassword: env.get("SEMIDX_BOOTSTRAP_ADMIN_PASSWORD", ""),
		CookieSecure:           env.get("SEMIDX_COOKIE_SECURE", "true") != "false",
		JWTSecret:              env.get("SEMIDX_JWT_SECRET", ""),
		LocalIndexPath:         resolveLocalIndex(env.get("SEMIDX_LOCAL_INDEX", "")),
		KeywordOnly:            env.get("SEMIDX_EMBED_MODE", "") == "none",
	}
}

// atoiDefault parses s as a positive int, returning def when empty or invalid.
func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
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
