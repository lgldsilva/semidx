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
	t.Setenv("SEMIDX_DB_DSN", "postgres://custom/db")
	t.Setenv("SEMIDX_LOCAL_INDEX", "")
	cfg := &config.Config{}
	if config.ZeroConfigRecommended(cfg, false) {
		t.Fatal("explicit SEMIDX_DB_DSN should disable zero-config")
	}
}

func TestZeroConfigRecommendedExplicitLocalIndex(t *testing.T) {
	t.Setenv("SEMIDX_DB_DSN", "")
	t.Setenv("SEMIDX_LOCAL_INDEX", "1")
	cfg := &config.Config{}
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
