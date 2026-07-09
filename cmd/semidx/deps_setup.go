package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/clientconfig"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
)

// setup loads config/client state and constructs the embedder chain for a run.
func (d *deps) setup(cmd *cobra.Command, forceLocal, keywordOnly bool, backendFlag string) error {
	d.cfg = config.Load()
	d.localIndexPath = d.cfg.LocalIndexPath
	if forceLocal && d.localIndexPath == "" {
		d.localIndexPath = config.DefaultLocalIndexPath()
	}
	d.keywordOnly = d.cfg.KeywordOnly || keywordOnly

	cc, err := clientconfig.Load()
	if err != nil {
		return fmt.Errorf("load client config: %w", err)
	}
	d.client = cc

	useRemote, err := resolveUseRemote(cc, forceLocal, backendFlag)
	if err != nil {
		return err
	}
	d.useRemote = useRemote
	// --backend local (without --local) still means "do not use the server".
	// Prefer an existing SEMIDX_LOCAL_INDEX; otherwise fall through to Postgres
	// (or zero-config SQLite) like a machine that never logged in.

	d.applyZeroConfigDefaults(cmd, forceLocal, keywordOnly)
	d.emb = embed.NewChainFromConfig(embedChainConfig(d.cfg))
	return nil
}

func (d *deps) applyZeroConfigDefaults(cmd *cobra.Command, forceLocal, keywordOnly bool) {
	if forceLocal || keywordOnly {
		return
	}
	if !config.ZeroConfigRecommended(d.cfg, d.remote()) {
		return
	}
	d.localIndexPath = config.DefaultLocalIndexPath()
	d.keywordOnly = true
	if cmd.Name() == "help" || cmd.Name() == "completion" {
		return
	}
	fmt.Fprintln(os.Stderr, "[info] no database or embedding provider configured — using local keyword-only mode (configure GEMINI_API_KEY or SEMIDX_DB_DSN for semantic search)")
}

func embedChainConfig(cfg *config.Config) embed.ChainConfig {
	return embed.ChainConfig{
		OllamaURL:          cfg.OllamaURL,
		OllamaURLs:         cfg.OllamaURLs,
		Provider:           cfg.Provider,
		Endpoint:           cfg.Endpoint,
		APIKey:             cfg.APIKey,
		GeminiAPIKey:       cfg.GeminiAPIKey,
		GeminiBaseURL:      cfg.GeminiBaseURL,
		GroqAPIKey:         cfg.GroqAPIKey,
		GroqBaseURL:        cfg.GroqBaseURL,
		OpenRouterAPIKey:   cfg.OpenRouterAPIKey,
		OpenRouterBaseURL:  cfg.OpenRouterBaseURL,
		OllamaCloudAPIKey:  cfg.OllamaCloudAPIKey,
		OllamaCloudBaseURL: cfg.OllamaCloudBaseURL,
		Privacy:            cfg.Privacy,
		CircuitThreshold:   cfg.EmbedCircuitThreshold,
		CircuitCooldown:    cfg.EmbedCircuitCooldown,
	}
}
