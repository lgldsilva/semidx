package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProjectEnv chdirs into a fresh dir holding a .env with the given body,
// and isolates the user config file.
func writeProjectEnv(t *testing.T, body string) {
	t.Helper()
	isolateUserConfig(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func noEnv(string) (string, bool) { return "", false }

// TestProjectEnvCannotRedirectCredentials: a .env in the working directory
// comes from whatever repo the user cloned. It must not be able to point an
// API key at an attacker host, swap keys, aim the DB/Ollama elsewhere, or
// turn privacy routing off — those settings only come from the real env or
// the user's own config file.
func TestProjectEnvCannotRedirectCredentials(t *testing.T) {
	writeProjectEnv(t, "SEMIDX_GEMINI_BASE_URL=https://evil.example\n"+
		"GEMINI_API_KEY=attacker-key\n"+
		"OLLAMA_URL=http://evil.example:11434\n"+
		"SEMIDX_OLLAMA_URLS=http://evil.example:11434\n"+
		"EMBED_ENDPOINT=https://evil.example/v1\n"+
		"EMBED_PROVIDER=openai\n"+
		"EMBED_PRIVACY=false\n"+
		"SEMIDX_DB_DSN=postgres://evil.example/db\n"+
		"SEMIDX_SECRET_BLOCK_EMBEDDING=false\n"+
		"SEMIDX_INDEX_WORKERS=7\n"+
		"SEMIDX_EMBED_MODE=none\n")
	if err := SetUserEnv("GEMINI_API_KEY", "user-key"); err != nil {
		t.Fatal(err)
	}
	if err := SetUserEnv("EMBED_PRIVACY", "true"); err != nil {
		t.Fatal(err)
	}
	if err := SetUserEnv("SEMIDX_SECRET_BLOCK_EMBEDDING", "true"); err != nil {
		t.Fatal(err)
	}
	cfg := LoadWithLookup(noEnv)
	if cfg.GeminiBaseURL != defaultGeminiBaseURL {
		t.Errorf("GeminiBaseURL = %q, project .env must not redirect it", cfg.GeminiBaseURL)
	}
	if cfg.GeminiAPIKey != "user-key" {
		t.Errorf("GeminiAPIKey = %q, want the user's key", cfg.GeminiAPIKey)
	}
	if cfg.OllamaURL != defaultOllamaURL || cfg.OllamaURLs != nil {
		t.Errorf("Ollama redirected: %q %v", cfg.OllamaURL, cfg.OllamaURLs)
	}
	if cfg.Endpoint != "" || cfg.Provider != "" {
		t.Errorf("custom provider from project .env: %q %q", cfg.Provider, cfg.Endpoint)
	}
	if !cfg.Privacy || !cfg.SecretBlockEmbedding {
		t.Error("project .env disabled privacy/secret blocking")
	}
	if cfg.DatabaseURLConfigured || cfg.DatabaseURL != defaultDatabaseURL {
		t.Errorf("DSN taken from project .env: %q", cfg.DatabaseURL)
	}
	// Harmless tuning keys keep working from the project .env.
	if cfg.IndexWorkers != 7 || !cfg.KeywordOnly {
		t.Errorf("benign project .env keys lost: workers=%d keywordOnly=%v", cfg.IndexWorkers, cfg.KeywordOnly)
	}
}

func TestProjectEnvRealEnvStillWins(t *testing.T) {
	writeProjectEnv(t, "SEMIDX_INDEX_WORKERS=7\n")
	cfg := LoadWithLookup(func(k string) (string, bool) {
		if k == "SEMIDX_GEMINI_BASE_URL" {
			return "https://proxy.internal", true
		}
		return "", false
	})
	if cfg.GeminiBaseURL != "https://proxy.internal" {
		t.Errorf("real env base URL ignored: %q", cfg.GeminiBaseURL)
	}
}

func TestEffectiveValueIgnoresSensitiveProjectEnv(t *testing.T) {
	writeProjectEnv(t, "SEMIDX_UPDATE_URL=https://evil.example\nSEMIDX_UPDATE_API=https://evil.example\n"+
		"SEMIDX_INSECURE=1\nSEMIDX_EMBED_MODE=none\n")
	t.Setenv("SEMIDX_UPDATE_URL", "")
	t.Setenv("SEMIDX_UPDATE_API", "")
	t.Setenv("SEMIDX_INSECURE", "")
	t.Setenv("SEMIDX_EMBED_MODE", "")
	for _, k := range []string{"SEMIDX_UPDATE_URL", "SEMIDX_UPDATE_API", "SEMIDX_INSECURE"} {
		if v := EffectiveValue(k); v != "" {
			t.Errorf("EffectiveValue(%s) = %q from project .env", k, v)
		}
	}
	if EffectiveValue("SEMIDX_EMBED_MODE") != "none" {
		t.Error("benign project .env key lost in EffectiveValue")
	}
}

func TestProjectEnvWarnsOnceAboutSemidxKeysOnly(t *testing.T) {
	writeProjectEnv(t, "GEMINI_API_KEY=x\nSEMIDX_GROQ_BASE_URL=https://evil\nSTRIPE_SECRET_KEY=app\n")
	var buf strings.Builder
	prevOut := projectEnvWarnOut
	projectEnvWarnOut = &buf
	projectEnvWarned.Store(false)
	t.Cleanup(func() { projectEnvWarnOut = prevOut })
	_ = LoadWithLookup(noEnv)
	_ = LoadWithLookup(noEnv)
	out := buf.String()
	if strings.Count(out, "[warn]") != 1 {
		t.Fatalf("want exactly one warning, got:\n%s", out)
	}
	if !strings.Contains(out, "GEMINI_API_KEY, SEMIDX_GROQ_BASE_URL") {
		t.Errorf("warning does not name the ignored keys:\n%s", out)
	}
	if strings.Contains(out, "STRIPE") {
		t.Errorf("warning names a non-semidx key:\n%s", out)
	}
}

func TestProjectEnvKeyDenied(t *testing.T) {
	for _, k := range []string{"SEMIDX_DB_DSN", "OPENROUTER_API_KEY", "SEMIDX_CHAT_BASE_URL", "OLLAMA_URL",
		"SEMIDX_OLLAMA_URLS", "EMBED_ENDPOINT", "SEMIDX_UPDATE_API", "SEMIDX_GITHUB_TOKEN", "SEMIDX_JWT_SECRET",
		"SEMIDX_JAVA_DECOMPILER", "SEMIDX_MCP_CLIENT_CONFIG"} {
		if !projectEnvKeyDenied(k) {
			t.Errorf("%s should be denied from a project .env", k)
		}
	}
	for _, k := range []string{"SEMIDX_LOCAL_INDEX", "SEMIDX_EMBED_MODE", "SEMIDX_INDEX_WORKERS",
		"SEMIDX_MAX_FILE_SIZE", "SEMIDX_CHAT_MODEL", "SEMIDX_MCP_TOOLS"} {
		if projectEnvKeyDenied(k) {
			t.Errorf("%s should stay allowed from a project .env", k)
		}
	}
}

func TestProjectEnvAllowsOnlyTighteningPrivacy(t *testing.T) {
	if !projectEnvAllowed("EMBED_PRIVACY", "true") || projectEnvAllowed("EMBED_PRIVACY", "false") {
		t.Error("EMBED_PRIVACY: only the tightening value may come from a project .env")
	}
	if projectEnvAllowed("GEMINI_API_KEY", "true") {
		t.Error("a credential must never be allowed")
	}
}
