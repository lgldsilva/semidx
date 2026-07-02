package config

import (
	"os"
	"path/filepath"
	"testing"
)

// clearEnv blanks every variable Load reads so values leaking from the host
// environment (or CI) cannot influence a test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SEMIDX_DB_DSN", "SEMIDX_OLLAMA_URL", "OLLAMA_URL",
		"EMBED_PROVIDER", "EMBED_ENDPOINT", "EMBED_API_KEY",
		"GEMINI_API_KEY", "GROQ_API_KEY", "OPENROUTER_API_KEY",
		"OLLAMA_CLOUD_API_KEY", "EMBED_PRIVACY",
	} {
		t.Setenv(key, "")
	}
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

func TestMissingDotEnvIsNotAnError(t *testing.T) {
	clearEnv(t)
	t.Chdir(t.TempDir())

	if cfg := Load(); cfg == nil {
		t.Fatal("Load returned nil without a .env file")
	}
}
