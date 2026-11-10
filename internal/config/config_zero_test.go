package config_test

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/config"
)

func TestZeroConfigRecommended(t *testing.T) {
	t.Setenv("SEMIDX_DB_DSN", "")
	t.Setenv("SEMIDX_LOCAL_INDEX", "")
	cfg := &config.Config{}
	if !config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("expected zero-config for empty config")
	}
	cfg.GeminiAPIKey = "key"
	if config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("provider configured should disable zero-config")
	}
	cfg.GeminiAPIKey = ""
	cfg.LocalIndexPath = "/tmp/x"
	if config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("local index set should disable zero-config")
	}
	cfg.LocalIndexPath = ""
	if !config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("expected zero-config again")
	}
	if config.ZeroConfigRecommended(cfg, true) {
		t.Fatal("remote server should disable zero-config")
	}
}

func TestZeroConfigRecommendedExplicitDSN(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	cfg := config.LoadWithLookup(envOnly(map[string]string{"SEMIDX_DB_DSN": "postgres://custom/db"}))
	if config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("explicit SEMIDX_DB_DSN should disable zero-config")
	}
}

func TestZeroConfigRecommendedExplicitLocalIndex(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	cfg := config.LoadWithLookup(envOnly(map[string]string{"SEMIDX_LOCAL_INDEX": "1"}))
	if config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("explicit SEMIDX_LOCAL_INDEX should disable zero-config")
	}
}

func TestHasConfiguredEmbeddingProvider(t *testing.T) {
	cfg := &config.Config{}
	if cfg.HasConfiguredEmbeddingProvider() {
		t.Fatal("empty config should have no provider")
	}
	cfg.GroqAPIKey = "x"
	if !cfg.HasConfiguredEmbeddingProvider() {
		t.Fatal("groq key should count")
	}
}

func TestZeroConfigRecommendedKeywordOnly(t *testing.T) {
	cfg := &config.Config{KeywordOnly: true}
	if config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("already keyword-only should not re-trigger zero-config")
	}
}

// TestZeroConfigRecommendedHonoursUserConfigDSN: a DSN persisted with
// `semidx config set` (user config file, not the process env) must stop the
// zero-config SQLite keyword-only fallback, just as `config list` reports
// Postgres as the active backend.
func TestZeroConfigRecommendedHonoursUserConfigDSN(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("SEMIDX_DB_DSN", "")
	t.Setenv("SEMIDX_LOCAL_INDEX", "")
	if err := config.SetUserEnv("SEMIDX_DB_DSN", "postgres://saved/db"); err != nil {
		t.Fatal(err)
	}
	cfg := config.LoadWithLookup(func(string) (string, bool) { return "", false })
	if config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("DSN from the user config file should disable zero-config")
	}
}

func TestZeroConfigRecommendedHonoursUserConfigLocalIndex(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("SEMIDX_DB_DSN", "")
	t.Setenv("SEMIDX_LOCAL_INDEX", "")
	if err := config.SetUserEnv("SEMIDX_LOCAL_INDEX", "1"); err != nil {
		t.Fatal(err)
	}
	cfg := config.LoadWithLookup(func(string) (string, bool) { return "", false })
	if config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("SEMIDX_LOCAL_INDEX from the user config file should disable zero-config")
	}
}

// envOnly is a lookup over a fixed map, isolating tests from the real env.
func envOnly(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}
