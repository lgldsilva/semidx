package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Deprecated: Prefer LoadWithLookup with mapLookup for hermetic tests that
// never touch the OS environment. clearEnv only clears a subset of all env
// vars Load reads, so values leaking from the host can still influence tests.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SEMIDX_DB_DSN", "SEMIDX_OLLAMA_URL", "OLLAMA_URL",
		"EMBED_PROVIDER", "EMBED_ENDPOINT", "EMBED_API_KEY",
		"GEMINI_API_KEY", "GROQ_API_KEY", "OPENROUTER_API_KEY",
		"OLLAMA_CLOUD_API_KEY", "EMBED_PRIVACY", "SEMIDX_INDEX_WORKERS",
		"SEMIDX_LOCAL_INDEX",
	} {
		t.Setenv(key, "")
	}
	// Isolate the user config layer (os.UserConfigDir honors XDG_CONFIG_HOME on
	// Linux) so the developer's real ~/.config/semidx/semidx.env can't leak in.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// mapLookup returns an envLookup func (matching os.LookupEnv's signature) that
// reads from the given map. Keys absent from the map return ("", false).
// Use this with [LoadWithLookup] for hermetic tests.
func mapLookup(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// slicesEqual reports whether two string slices have the same length and
// elements.
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeDotEnv(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func TestDefaults(t *testing.T) {
	clearEnv(t)
	t.Chdir(t.TempDir())

	cfg := Load()

	if cfg.DatabaseURL != defaultDatabaseURL {
		t.Errorf("DatabaseURL = %q, want default %q", cfg.DatabaseURL, defaultDatabaseURL)
	}
	if cfg.OllamaURL != defaultOllamaURL {
		t.Errorf("OllamaURL = %q, want default %q", cfg.OllamaURL, defaultOllamaURL)
	}
	if cfg.Provider != "" || cfg.APIKey != "" || cfg.GeminiAPIKey != "" {
		t.Errorf("expected empty provider settings, got %+v", cfg)
	}
	if cfg.Privacy {
		t.Error("Privacy = true, want false by default")
	}
}

func TestDotEnvFile(t *testing.T) {
	clearEnv(t)
	writeDotEnv(t, `
# comment line
SEMIDX_DB_DSN=postgres://u:p@dbhost:5432/db
GEMINI_API_KEY=file-key

malformed line without equals
EMBED_PRIVACY=true
`)

	cfg := Load()

	if want := "postgres://u:p@dbhost:5432/db"; cfg.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
	if cfg.GeminiAPIKey != "file-key" {
		t.Errorf("GeminiAPIKey = %q, want %q", cfg.GeminiAPIKey, "file-key")
	}
	if !cfg.Privacy {
		t.Error("Privacy = false, want true from .env")
	}
}

func TestRealEnvOverridesDotEnv(t *testing.T) {
	clearEnv(t)
	writeDotEnv(t, "GEMINI_API_KEY=file-key\nSEMIDX_DB_DSN=postgres://file/db\n")
	t.Setenv("GEMINI_API_KEY", "env-key")

	cfg := Load()

	if cfg.GeminiAPIKey != "env-key" {
		t.Errorf("GeminiAPIKey = %q, want real env to win over .env", cfg.GeminiAPIKey)
	}
	if want := "postgres://file/db"; cfg.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want .env value %q", cfg.DatabaseURL, want)
	}
}

func TestOllamaURLPrecedence(t *testing.T) {
	t.Run("legacy env var is honored", func(t *testing.T) {
		clearEnv(t)
		t.Chdir(t.TempDir())
		t.Setenv("OLLAMA_URL", "http://legacy:11434")

		if got := Load().OllamaURL; got != "http://legacy:11434" {
			t.Errorf("OllamaURL = %q, want legacy OLLAMA_URL value", got)
		}
	})

	t.Run("SEMIDX_OLLAMA_URL wins over legacy", func(t *testing.T) {
		clearEnv(t)
		t.Chdir(t.TempDir())
		t.Setenv("OLLAMA_URL", "http://legacy:11434")
		t.Setenv("SEMIDX_OLLAMA_URL", "http://new:11434")

		if got := Load().OllamaURL; got != "http://new:11434" {
			t.Errorf("OllamaURL = %q, want SEMIDX_OLLAMA_URL value", got)
		}
	})

	t.Run("real legacy env wins over .env primary", func(t *testing.T) {
		clearEnv(t)
		writeDotEnv(t, "SEMIDX_OLLAMA_URL=http://from-file:11434\n")
		t.Setenv("OLLAMA_URL", "http://legacy:11434")

		if got := Load().OllamaURL; got != "http://legacy:11434" {
			t.Errorf("OllamaURL = %q, want real env (any name) to win over .env", got)
		}
	})
}

func TestPrivacyParsing(t *testing.T) {
	for value, want := range map[string]bool{"true": true, "TRUE": false, "1": false, "": false} {
		clearEnv(t)
		t.Chdir(t.TempDir())
		t.Setenv("EMBED_PRIVACY", value)

		if got := Load().Privacy; got != want {
			t.Errorf("Privacy with EMBED_PRIVACY=%q = %v, want %v", value, got, want)
		}
	}
}

func TestIndexWorkers(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		clearEnv(t)
		t.Chdir(t.TempDir())
		if got := Load().IndexWorkers; got != defaultIndexWorkers {
			t.Errorf("IndexWorkers = %d, want default %d", got, defaultIndexWorkers)
		}
	})
	t.Run("env override", func(t *testing.T) {
		clearEnv(t)
		t.Chdir(t.TempDir())
		t.Setenv("SEMIDX_INDEX_WORKERS", "12")
		if got := Load().IndexWorkers; got != 12 {
			t.Errorf("IndexWorkers = %d, want 12", got)
		}
	})
	t.Run("invalid falls back to default", func(t *testing.T) {
		clearEnv(t)
		t.Chdir(t.TempDir())
		t.Setenv("SEMIDX_INDEX_WORKERS", "nonsense")
		if got := Load().IndexWorkers; got != defaultIndexWorkers {
			t.Errorf("IndexWorkers = %d, want default on invalid input", got)
		}
	})
}

func TestMissingDotEnvIsNotAnError(t *testing.T) {
	clearEnv(t)
	t.Chdir(t.TempDir())

	if cfg := Load(); cfg == nil {
		t.Fatal("Load returned nil without a .env file")
	}
}

func TestResolveLocalIndex(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/data") // os.UserCacheDir honors this on Linux
	cases := map[string]string{
		"":               "",
		"true":           "/data/semidx/index.db",
		"1":              "/data/semidx/index.db",
		"/custom/idx.db": "/custom/idx.db",
		"./relative.db":  "./relative.db",
	}
	for in, want := range cases {
		if got := resolveLocalIndex(in); got != want {
			t.Errorf("resolveLocalIndex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadLocalIndexFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("SEMIDX_LOCAL_INDEX", "/tmp/x.db")
	if got := Load().LocalIndexPath; got != "/tmp/x.db" {
		t.Errorf("LocalIndexPath = %q, want /tmp/x.db", got)
	}
}

func TestAgentActionsResolution(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Absent → safe default "off".
	if cfg := LoadWithLookup(mapLookup(map[string]string{})); cfg.AgentActions != "off" {
		t.Errorf("default AgentActions = %q, want %q", cfg.AgentActions, "off")
	}
	// Present → honored, lowercased and trimmed so ParseActionPolicy matches.
	cfg := LoadWithLookup(mapLookup(map[string]string{"SEMIDX_AGENT_ACTIONS": "  Execute  "}))
	if cfg.AgentActions != "execute" {
		t.Errorf("AgentActions = %q, want %q (normalized)", cfg.AgentActions, "execute")
	}
}

func TestLoadWithLookupHermetic(t *testing.T) {
	// No real OS env interaction: all values come exclusively from the map.
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	env := map[string]string{
		"SEMIDX_DB_DSN":                   "postgres://test:test@localhost:5432/test",
		"SEMIDX_OLLAMA_URL":               "http://test-ollama:11434",
		"GEMINI_API_KEY":                  "test-gemini-key",
		"EMBED_PRIVACY":                   "true",
		"SEMIDX_INDEX_WORKERS":            "8",
		"SEMIDX_EMBED_BATCH_SIZE":         "16",
		"SEMIDX_MAX_FILE_SIZE":            "2097152",
		"SEMIDX_MAX_CHUNKS_PER_FILE":      "64",
		"SEMIDX_MAX_CHUNKS_PER_PROJECT":   "1000",
		"SEMIDX_LISTEN_ADDR":              ":9090",
		"SEMIDX_BOOTSTRAP_TOKEN":          "bootstrap",
		"SEMIDX_DATA_DIR":                 "/tmp/semidx",
		"SEMIDX_BOOTSTRAP_ADMIN_USER":     "admin",
		"SEMIDX_BOOTSTRAP_ADMIN_PASSWORD": "secret",
		"SEMIDX_COOKIE_SECURE":            "false",
		"SEMIDX_JWT_SECRET":               "jwt-secret",
		"SEMIDX_CSRF_KEY":                 "csrf-key",
		"SEMIDX_OLLAMA_URLS":              "http://ollama1:11434,http://ollama2:11434",
		"SEMIDX_EMBED_CIRCUIT_THRESHOLD":  "5",
		"SEMIDX_EMBED_CIRCUIT_COOLDOWN":   "60s",
	}

	cfg := LoadWithLookup(mapLookup(env))

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"DatabaseURL", cfg.DatabaseURL, "postgres://test:test@localhost:5432/test"},
		{"OllamaURL", cfg.OllamaURL, "http://test-ollama:11434"},
		{"GeminiAPIKey", cfg.GeminiAPIKey, "test-gemini-key"},
		{"Privacy", cfg.Privacy, true},
		{"IndexWorkers", cfg.IndexWorkers, 8},
		{"EmbedBatchSize", cfg.EmbedBatchSize, 16},
		{"MaxFileSize", cfg.MaxFileSize, 2097152},
		{"MaxChunksPerFile", cfg.MaxChunksPerFile, 64},
		{"MaxChunksPerProject", cfg.MaxChunksPerProject, 1000},
		{"ListenAddr", cfg.ListenAddr, ":9090"},
		{"BootstrapToken", cfg.BootstrapToken, "bootstrap"},
		{"DataDir", cfg.DataDir, "/tmp/semidx"},
		{"BootstrapAdminUser", cfg.BootstrapAdminUser, "admin"},
		{"BootstrapAdminPassword", cfg.BootstrapAdminPassword, "secret"},
		{"CookieSecure", cfg.CookieSecure, false},
		{"JWTSecret", cfg.JWTSecret, "jwt-secret"},
		{"CSRFKey", cfg.CSRFKey, "csrf-key"},
		{"KeywordOnly", cfg.KeywordOnly, false},
		{"EmbedCircuitThreshold", cfg.EmbedCircuitThreshold, 5},
		{"EmbedCircuitCooldown", cfg.EmbedCircuitCooldown, 60 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v (%T), want %v (%T)", tc.got, tc.got, tc.want, tc.want)
			}
		})
	}

	// LocalIndexPath: SEMIDX_DB_DSN is set, so local index is disabled.
	if cfg.LocalIndexPath != "" {
		t.Errorf("LocalIndexPath = %q, want empty (DSN takes precedence)", cfg.LocalIndexPath)
	}

	// OllamaURLs from comma-separated list.
	if want := []string{"http://ollama1:11434", "http://ollama2:11434"}; !slicesEqual(cfg.OllamaURLs, want) {
		t.Errorf("OllamaURLs = %v, want %v", cfg.OllamaURLs, want)
	}
}

func TestParseCommaSep(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"  ", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"http://a:11434,http://b:11434", []string{"http://a:11434", "http://b:11434"}},
		{"a,,b", []string{"a", "b"}},
		{",,,", nil},
	}
	for _, tt := range tests {
		got := parseCommaSep(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseCommaSep(%q) len = %d, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		if tt.want == nil && got == nil {
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseCommaSep(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}
