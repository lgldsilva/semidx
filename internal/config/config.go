// Package config resolves all runtime configuration for semidx.
//
// Precedence, highest first:
//  1. real environment variables
//  2. a .env file in the current working directory
//  3. the persistent user config file (~/.config/semidx/semidx.env), written
//     by `semidx config set`
//  4. built-in defaults
//
// The .env parser intentionally mirrors the PoC's legacy behavior: one
// KEY=VALUE per line, blank lines and "#" comments skipped, no quote
// stripping, and values never override variables already exported in the
// real environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/xdg"
)

const (
	// Credential-free local-dev default: points at the dev Postgres but carries no
	// password in source. Real deployments set SEMIDX_DB_DSN (the compose does);
	// a local dev supplies credentials via SEMIDX_DB_DSN or the standard PG* env.
	defaultDatabaseURL         = "postgres://localhost:55432/semantic_indexer"
	defaultOllamaURL           = "http://localhost:11434"
	defaultGeminiBaseURL       = "https://generativelanguage.googleapis.com/v1beta/openai"
	defaultGroqBaseURL         = "https://api.groq.com/openai/v1"
	defaultOpenRouterURL       = "https://openrouter.ai/api/v1"
	defaultOllamaCloudURL      = "https://ollama.com/v1"
	defaultIndexWorkers        = 4
	defaultEmbedBatchSize      = 8
	defaultMaxFileSize         = 1024 * 1024 // 1MB
	defaultMaxChunksPerFile    = 32
	defaultMaxChunksPerProject = 0 // 0 = unlimited
	defaultListenAddr          = ":8080"
	defaultDataDir             = "/var/lib/semidx"
)

// Config holds every runtime setting the CLI and MCP server need.
type Config struct {
	// DatabaseURL is the PostgreSQL/pgvector DSN (SEMIDX_DB_DSN).
	DatabaseURL string
	// OllamaURL is the local Ollama endpoint (SEMIDX_OLLAMA_URL, legacy OLLAMA_URL).
	OllamaURL string
	// OllamaURLs, when non-empty, enables parallel embedding mode: each URL
	// becomes its own pool entry (round-robin). Cloud providers (Gemini, Groq,
	// OpenRouter, OllamaCloud) are bundled as one additional pool entry.
	// (SEMIDX_OLLAMA_URLS, comma-separated). Overrides single SEMIDX_OLLAMA_URL
	// when set.
	OllamaURLs []string

	// Provider optionally prepends a custom provider to the embedding chain
	// (EMBED_PROVIDER: "openai" or "ollama"), served at Endpoint with APIKey.
	Provider string
	Endpoint string
	APIKey   string

	GeminiAPIKey       string
	GeminiBaseURL      string
	GroqAPIKey         string
	GroqBaseURL        string
	OpenRouterAPIKey   string
	OpenRouterBaseURL  string
	OllamaCloudAPIKey  string
	OllamaCloudBaseURL string

	// Privacy forces local-only embedding providers (EMBED_PRIVACY=true).
	Privacy bool

	// IndexWorkers is how many files are indexed concurrently
	// (SEMIDX_INDEX_WORKERS). Defaults to defaultIndexWorkers.
	IndexWorkers int
	// EmbedBatchSize controls how many texts are sent per embedding API call
	// (SEMIDX_EMBED_BATCH_SIZE). Defaults to defaultEmbedBatchSize.
	EmbedBatchSize int
	// MaxFileSize is the largest file (bytes) the indexer will process
	// (SEMIDX_MAX_FILE_SIZE). Files larger than this are silently skipped.
	// Defaults to 1MB.
	MaxFileSize int
	// MaxChunksPerFile caps how many chunks a single file can produce
	// (SEMIDX_MAX_CHUNKS_PER_FILE). Defaults to 32.
	MaxChunksPerFile int
	// MaxChunksPerProject caps the total number of chunks a project may have
	// (SEMIDX_MAX_CHUNKS_PER_PROJECT). 0 = unlimited. Default is unlimited.
	MaxChunksPerProject int

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
	// CSRFKey is the secret used to derive CSRF tokens for the web admin
	// (SEMIDX_CSRF_KEY). When empty, a random key is generated on each restart.
	CSRFKey string
	// LocalIndexPath, when non-empty, makes the CLI index and search a local
	// SQLite file instead of PostgreSQL (SEMIDX_LOCAL_INDEX: a path, or a truthy
	// value to use the default location). Empty means server/Postgres mode.
	LocalIndexPath string
	// KeywordOnly indexes and searches without any embedding model — text is
	// stored and matched by keyword (SEMIDX_EMBED_MODE=none). The zero-dependency
	// baseline for a machine with no GPU, API key or Ollama.
	KeywordOnly bool

	// EmbedCircuitThreshold is the consecutive failure count before the circuit
	// breaker opens for a provider (SEMIDX_EMBED_CIRCUIT_THRESHOLD, default 3).
	EmbedCircuitThreshold int
	// EmbedCircuitCooldown is how long the circuit stays open before allowing a
	// probe request (SEMIDX_EMBED_CIRCUIT_COOLDOWN, default "30s").
	EmbedCircuitCooldown time.Duration

	// GitAllowFile permits file:// git URLs for server-side sync (SEMIDX_GIT_ALLOW_FILE).
	GitAllowFile bool
	// MetricsToken, when set, requires Bearer auth on GET /metrics (SEMIDX_METRICS_TOKEN).
	MetricsToken string

	// SecretScan enables gitleaks secret scanning during indexing
	// (SEMIDX_SECRET_SCAN). When true, each file is scanned and findings are
	// logged. When SECRET_BLOCK_EMBEDDING is also true, files with detected
	// secrets are stored text-only (no embedding).
	SecretScan bool
	// SecretBlockEmbedding prevents embedding files with detected secrets
	// (SEMIDX_SECRET_BLOCK_EMBEDDING). Implies SEMIDX_SECRET_SCAN=true.
	SecretBlockEmbedding bool
}

// DefaultLocalIndexPath is the standalone index location. It uses the OS-native
// per-user data dir (os.UserCacheDir: %LocalAppData% on Windows, ~/Library/Caches
// on macOS, $XDG_CACHE_HOME or ~/.cache on Linux), so semidx works cross-platform.
func DefaultLocalIndexPath() string {
	return xdg.DefaultLocalIndexPath()
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

// Clone returns a deep copy of c. The slice fields (OllamaURLs) are copied so
// callers can mutate the clone without affecting the original. Use Clone when a
// command needs a tweaked config (e.g. overriding LocalIndexPath) instead of
// mutating the shared loaded config — that keeps the loaded instance read-only
// and safe for concurrent readers.
func (c *Config) Clone() *Config {
	cp := *c
	if c.OllamaURLs != nil {
		cp.OllamaURLs = append([]string(nil), c.OllamaURLs...)
	}
	return &cp
}

// HasConfiguredEmbeddingProvider reports whether any cloud or custom embedding
// provider API key is set. Local Ollama at the default URL does not count — it
// may be absent on a fresh machine.
func (c *Config) HasConfiguredEmbeddingProvider() bool {
	return c.Provider != "" ||
		c.APIKey != "" ||
		c.GeminiAPIKey != "" ||
		c.GroqAPIKey != "" ||
		c.OpenRouterAPIKey != "" ||
		c.OllamaCloudAPIKey != ""
}

// ZeroConfigRecommended reports whether the CLI should enable standalone local
// keyword-only mode because no database, remote server, or embedding provider
// has been explicitly configured.
func ZeroConfigRecommended(cfg *Config, remoteServer bool) bool {
	if cfg.KeywordOnly || cfg.LocalIndexPath != "" {
		return false
	}
	if remoteServer {
		return false
	}
	if os.Getenv("SEMIDX_DB_DSN") != "" || os.Getenv("SEMIDX_LOCAL_INDEX") != "" {
		return false
	}
	return !cfg.HasConfiguredEmbeddingProvider()
}

// Load resolves the configuration using the real OS environment. A missing
// or unreadable .env file is not an error; malformed lines in it are skipped.
// The persistent user config file (see UserEnvPath) is layered in at the
// lowest file precedence.
func Load() *Config {
	return LoadWithLookup(os.LookupEnv)
}

// LoadWithLookup resolves the configuration using the provided envLookup
// function instead of os.LookupEnv. The .env file and user config file layers
// are still read from disk; only the "real environment" level is replaced.
// Use this in tests with [mapLookup] to avoid depending on OS env vars.
func LoadWithLookup(envLookup func(string) (string, bool)) *Config {
	paths := []string{".env"}
	if p, err := UserEnvPath(); err == nil {
		paths = append(paths, p)
	}
	env := newResolverWithLookup(envLookup, paths...)
	return &Config{
		DatabaseURL:         env.get("SEMIDX_DB_DSN", defaultDatabaseURL),
		OllamaURL:           env.first("SEMIDX_OLLAMA_URL", "OLLAMA_URL", defaultOllamaURL),
		OllamaURLs:          parseCommaSep(env.get("SEMIDX_OLLAMA_URLS", "")),
		Provider:            env.get("EMBED_PROVIDER", ""),
		Endpoint:            env.get("EMBED_ENDPOINT", ""),
		APIKey:              env.get("EMBED_API_KEY", ""),
		GeminiAPIKey:        env.get("GEMINI_API_KEY", ""),
		GeminiBaseURL:       env.get("SEMIDX_GEMINI_BASE_URL", defaultGeminiBaseURL),
		GroqAPIKey:          env.get("GROQ_API_KEY", ""),
		GroqBaseURL:         env.get("SEMIDX_GROQ_BASE_URL", defaultGroqBaseURL),
		OpenRouterAPIKey:    env.get("OPENROUTER_API_KEY", ""),
		OpenRouterBaseURL:   env.get("SEMIDX_OPENROUTER_BASE_URL", defaultOpenRouterURL),
		OllamaCloudAPIKey:   env.get("OLLAMA_CLOUD_API_KEY", ""),
		OllamaCloudBaseURL:  env.get("SEMIDX_OLLAMA_CLOUD_BASE_URL", defaultOllamaCloudURL),
		Privacy:             env.get("EMBED_PRIVACY", "") == "true",
		IndexWorkers:        atoiDefault(env.get("SEMIDX_INDEX_WORKERS", ""), defaultIndexWorkers),
		EmbedBatchSize:      atoiDefault(env.get("SEMIDX_EMBED_BATCH_SIZE", ""), defaultEmbedBatchSize),
		MaxFileSize:         atoiDefault(env.get("SEMIDX_MAX_FILE_SIZE", ""), defaultMaxFileSize),
		MaxChunksPerFile:    atoiDefault(env.get("SEMIDX_MAX_CHUNKS_PER_FILE", ""), defaultMaxChunksPerFile),
		MaxChunksPerProject: atoiDefault(env.get("SEMIDX_MAX_CHUNKS_PER_PROJECT", ""), defaultMaxChunksPerProject),
		ListenAddr:          env.get("SEMIDX_LISTEN_ADDR", defaultListenAddr),
		BootstrapToken:      env.get("SEMIDX_BOOTSTRAP_TOKEN", ""),
		DataDir:             env.get("SEMIDX_DATA_DIR", defaultDataDir),

		BootstrapAdminUser:     env.get("SEMIDX_BOOTSTRAP_ADMIN_USER", "admin"),
		BootstrapAdminPassword: env.get("SEMIDX_BOOTSTRAP_ADMIN_PASSWORD", ""),
		CookieSecure:           env.get("SEMIDX_COOKIE_SECURE", "true") != "false",
		JWTSecret:              env.get("SEMIDX_JWT_SECRET", ""),
		CSRFKey:                env.get("SEMIDX_CSRF_KEY", ""),
		// If a Postgres DSN is explicitly configured, it takes precedence over
		// SEMIDX_LOCAL_INDEX (Postgres (configured) > SQLite > Postgres (default)).
		LocalIndexPath: func() string {
			if env.get("SEMIDX_DB_DSN", "") != "" {
				return ""
			}
			return resolveLocalIndex(env.get("SEMIDX_LOCAL_INDEX", ""))
		}(),
		KeywordOnly: env.get("SEMIDX_EMBED_MODE", "") == "none",

		EmbedCircuitThreshold: func() int {
			return atoiDefault(env.get("SEMIDX_EMBED_CIRCUIT_THRESHOLD", ""), 3)
		}(),
		EmbedCircuitCooldown: func() time.Duration {
			s := env.get("SEMIDX_EMBED_CIRCUIT_COOLDOWN", "")
			if d, err := time.ParseDuration(s); err == nil && d > 0 {
				return d
			}
			return 30 * time.Second
		}(),
		GitAllowFile: env.get("SEMIDX_GIT_ALLOW_FILE", "") == "true",
		MetricsToken: env.get("SEMIDX_METRICS_TOKEN", ""),

		SecretScan:           env.get("SEMIDX_SECRET_SCAN", "") == "true",
		SecretBlockEmbedding: env.get("SEMIDX_SECRET_BLOCK_EMBEDDING", "") == "true",
	}
}

// KeySpec documents a configuration key the CLI knows how to manage.
type KeySpec struct {
	Name   string
	Desc   string
	Secret bool // true → masked in listings, stored under 0600
}

// KnownKeys is the curated set surfaced by `semidx config` for discoverability.
// Any other key can still be set (with a warning); this list drives help,
// validation hints and secret masking. It spans the three CLI storage backends
// (Postgres DSN, local SQLite, remote server), the embedding providers, and the
// server-only (serve) settings.
var KnownKeys = []KeySpec{
	// Storage backend — pick ONE; precedence at run time is remote > Postgres (configured) > SQLite > Postgres (default).
	{"SEMIDX_DB_DSN", "PostgreSQL/pgvector DSN — connect the CLI straight to Postgres", true},
	{"SEMIDX_LOCAL_INDEX", "Standalone SQLite index (a path, or 1/true for the default location)", false},
	{"SEMIDX_EMBED_MODE", "Set to \"none\" for keyword-only indexing (no embedding model)", false},
	// Embedding providers (chain, highest priority first; local Ollama is the fallback).
	{"GEMINI_API_KEY", "Google Gemini embedding key", true},
	{"SEMIDX_GEMINI_BASE_URL", "Gemini API base URL", false},
	{"GROQ_API_KEY", "Groq embedding key", true},
	{"SEMIDX_GROQ_BASE_URL", "Groq API base URL", false},
	{"OPENROUTER_API_KEY", "OpenRouter embedding key", true},
	{"SEMIDX_OPENROUTER_BASE_URL", "OpenRouter API base URL", false},
	{"OLLAMA_CLOUD_API_KEY", "Ollama Cloud embedding key", true},
	{"SEMIDX_OLLAMA_CLOUD_BASE_URL", "Ollama Cloud API base URL", false},
	{"SEMIDX_OLLAMA_URL", "Local Ollama endpoint (embedding fallback)", false},
	{"SEMIDX_OLLAMA_URLS", "Comma-separated Ollama URLs for parallel embedding pool", false},
	{"EMBED_PROVIDER", "Custom provider prepended to the chain (openai|ollama)", false},
	{"EMBED_ENDPOINT", "Custom provider endpoint URL", false},
	{"EMBED_API_KEY", "Custom provider API key", true},
	{"EMBED_PRIVACY", "Force local-only embedding providers (true)", false},
	{"SEMIDX_EMBED_CIRCUIT_THRESHOLD", "Consecutive failures before circuit breaker opens (default 3)", false},
	{"SEMIDX_EMBED_CIRCUIT_COOLDOWN", "How long the circuit stays open (e.g. 30s, 1m, default 30s)", false},
	{"SEMIDX_INDEX_WORKERS", "Concurrent index workers (positive int)", false},
	{"SEMIDX_EMBED_BATCH_SIZE", "Texts per embedding API call (positive int)", false},
	{"SEMIDX_MAX_FILE_SIZE", "Largest file the indexer processes (bytes, positive int)", false},
	{"SEMIDX_MAX_CHUNKS_PER_FILE", "Maximum chunks a single file can produce (positive int)", false},
	{"SEMIDX_MAX_CHUNKS_PER_PROJECT", "Maximum chunks per project (0=unlimited)", false},
	{"SEMIDX_JAVA_DECOMPILER", "External Java decompiler command for .class in JARs", false},
	// Self-update (semidx upgrade) — override to point at a different release host.
	{"SEMIDX_UPDATE_API", "Releases API base for `semidx upgrade` (default: homelab Gitea)", false},
	{"SEMIDX_UPDATE_URL", "Release download base for `semidx upgrade` (default: homelab Gitea)", false},
	{"SEMIDX_UPDATE_TOKEN", "Token for `semidx upgrade` against a private release host", true},
	{"SEMIDX_INSECURE", "Skip TLS verification for update downloads (1 = self-signed CA)", false},
	// Server-only (semidx serve).
	{"SEMIDX_LISTEN_ADDR", "Server bind address, e.g. :8080 (serve)", false},
	{"SEMIDX_DATA_DIR", "Where the server clones git projects (serve)", false},
	{"SEMIDX_JWT_SECRET", "HS256 secret enabling JWT control tokens (serve)", true},
	{"SEMIDX_CSRF_KEY", "HMAC key for web-admin CSRF tokens; persistent across restarts (serve)", true},
	{"SEMIDX_GIT_ALLOW_FILE", "Allow file:// git URLs for server-side git sync (serve)", false},
	{"SEMIDX_METRICS_TOKEN", "Bearer token required for GET /metrics when set (serve)", true},
	// Secret scanning.
	{"SEMIDX_SECRET_SCAN", "Enable gitleaks secret scanning during indexing (true)", false},
	{"SEMIDX_SECRET_BLOCK_EMBEDDING", "Prevent embedding files with detected secrets (true)", false},
	{"SEMIDX_COOKIE_SECURE", "Secure flag on web-admin cookies; false only over HTTP (serve)", false},
}

// IsSecret reports whether a key holds a credential (masked in listings). Known
// keys use their spec; unknown keys are treated as secret when the name hints
// at one, erring toward masking.
func IsSecret(key string) bool {
	for _, k := range KnownKeys {
		if k.Name == key {
			return k.Secret
		}
	}
	up := strings.ToUpper(key)
	for _, hint := range []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "DSN"} {
		if strings.Contains(up, hint) {
			return true
		}
	}
	return false
}

// UserEnvPath is the persistent per-user config file (KEY=VALUE), honoring
// XDG_CONFIG_HOME (usually ~/.config/semidx/semidx.env). It is the lowest-
// precedence file layer, below a project .env and the real environment.
func UserEnvPath() (string, error) {
	return xdg.UserEnvPath()
}

// LoadUserEnv returns the persisted key/value pairs (empty when the file is
// absent).
func LoadUserEnv() (map[string]string, error) {
	p, err := UserEnvPath()
	if err != nil {
		return nil, err
	}
	return parseEnvFile(p), nil
}

// SetUserEnv persists key=value in the user config file, preserving the other
// entries. The file is written 0600 (it may hold secrets) under a 0700 dir.
func SetUserEnv(key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("empty config key")
	}
	if strings.ContainsAny(key, "=\n") || strings.Contains(value, "\n") {
		return fmt.Errorf("key/value must not contain '=' or newlines")
	}
	m, err := LoadUserEnv()
	if err != nil {
		return err
	}
	m[key] = value
	return writeUserEnv(m)
}

// UnsetUserEnv removes a key from the user config file. Removing an absent key
// is a no-op (no error).
func UnsetUserEnv(key string) error {
	m, err := LoadUserEnv()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return writeUserEnv(m)
}

func writeUserEnv(m map[string]string) error {
	p, err := UserEnvPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# semidx config — managed by `semidx config`; edits are preserved by key.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, m[k])
	}
	return os.WriteFile(p, []byte(b.String()), 0o600)
}

// EffectiveValue resolves a key through the full precedence chain (real env >
// project .env > user config file), returning "" when unset at every level.
func EffectiveValue(key string) string {
	paths := []string{".env"}
	if p, err := UserEnvPath(); err == nil {
		paths = append(paths, p)
	}
	return newResolver(paths...).get(key, "")
}

// atoiDefault parses s as a positive int, returning def when empty or invalid.
func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

// parseCommaSep splits s by comma, trims whitespace, and drops empty entries.
// Returns nil when the input is blank.
func parseCommaSep(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolver looks a key up in the real environment first, then in each parsed
// env-file layer in precedence order, then falls back to the given default.
// Empty values count as unset at every level.
type resolver struct {
	lookup func(string) (string, bool) // env var lookup (e.g. os.LookupEnv)
	layers []map[string]string         // highest file precedence first
}

func newResolver(paths ...string) *resolver {
	return newResolverWithLookup(os.LookupEnv, paths...)
}

func newResolverWithLookup(lookup func(string) (string, bool), paths ...string) *resolver {
	r := &resolver{lookup: lookup}
	for _, p := range paths {
		r.layers = append(r.layers, parseEnvFile(p))
	}
	return r
}

// parseEnvFile reads a KEY=VALUE file into a map. A missing or unreadable file
// yields an empty map; malformed lines and comments are skipped.
func parseEnvFile(path string) map[string]string {
	vars := make(map[string]string)
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return vars
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
	return vars
}

func (r *resolver) get(key, def string) string {
	if v, ok := r.lookup(key); ok && v != "" {
		return v
	}
	for _, m := range r.layers {
		if v := m[key]; v != "" {
			return v
		}
	}
	return def
}

// first returns the value of the first key that resolves at the highest
// available level, checking both keys against the real environment before
// descending through the file layers.
func (r *resolver) first(primary, legacy, def string) string {
	if v, ok := r.lookup(primary); ok && v != "" {
		return v
	}
	if v, ok := r.lookup(legacy); ok && v != "" {
		return v
	}
	for _, m := range r.layers {
		if v := m[primary]; v != "" {
			return v
		}
		if v := m[legacy]; v != "" {
			return v
		}
	}
	return def
}
