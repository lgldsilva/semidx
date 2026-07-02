// Command semidx is the semantic code-search CLI (and, via `serve` in a later
// phase, the server). Subcommands are built with cobra; shared runtime
// dependencies (config, database, embedder) are constructed once per invocation.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

// deps are the runtime dependencies shared by all subcommands, built in the
// root command's PersistentPreRunE and torn down in PersistentPostRun.
type deps struct {
	cfg *config.Config
	db  store.Store
	emb embed.Embedder
}

func main() {
	// Cancel the context on Ctrl-C / SIGTERM so a long index/search stops cleanly.
	// Memory is bounded via GOMEMLIMIT (a soft limit the runtime respects) — set
	// it in the environment (e.g. GOMEMLIMIT=450MiB) instead of hardcoding a cap.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	d := &deps{}
	root := &cobra.Command{
		Use:           "semidx",
		Short:         "Self-hosted semantic code search",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			d.cfg = config.Load()
			d.emb = buildChain(d.cfg)
			db, err := store.NewPgStore(cmd.Context(), d.cfg.DatabaseURL)
			if err != nil {
				return fmt.Errorf("connect to database: %w", err)
			}
			d.db = db
			return nil
		},
		PersistentPostRun: func(_ *cobra.Command, _ []string) {
			if d.db != nil {
				d.db.Close()
			}
		},
	}
	root.AddCommand(
		newIndexCmd(d),
		newSearchCmd(d),
		newSgrepCmd(d),
		newModelsCmd(d),
		newDropCmd(d),
		newServeCmd(d),
		newMCPCmd(d),
	)
	return root
}

func projectNameFromPath(path string) string {
	path = strings.TrimRight(path, "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func buildChain(cfg *config.Config) embed.Embedder {
	var providers []embed.ProviderInstance

	if cfg.GeminiAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "gemini",
			Embedder: embed.NewOpenAIClient("https://generativelanguage.googleapis.com/v1beta/openai", cfg.GeminiAPIKey),
			Local:    false,
		})
	}
	if cfg.GroqAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "groq",
			Embedder: embed.NewOpenAIClient("https://api.groq.com/openai/v1", cfg.GroqAPIKey),
			Local:    false,
		})
	}
	if cfg.OpenRouterAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "openrouter",
			Embedder: embed.NewOpenAIClient("https://openrouter.ai/api/v1", cfg.OpenRouterAPIKey),
			Local:    false,
		})
	}
	if cfg.OllamaCloudAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "ollama-cloud",
			Embedder: embed.NewOpenAIClient("https://ollama.com/v1", cfg.OllamaCloudAPIKey),
			Local:    false,
		})
	}

	// Local Ollama is always the final fallback.
	providers = append(providers, embed.ProviderInstance{
		Name:     "ollama",
		Embedder: embed.NewOllamaClient(cfg.OllamaURL),
		Local:    true,
	})

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
		var custom embed.Embedder
		if cfg.Provider == "openai" {
			custom = embed.NewOpenAIClient(endpoint, cfg.APIKey)
		} else {
			custom = embed.NewOllamaClient(endpoint)
		}
		providers = append([]embed.ProviderInstance{{
			Name:     "custom",
			Embedder: custom,
			Local:    cfg.Provider == "ollama",
		}}, providers...)
	}

	return embed.NewChainEmbedder(providers, cfg.Privacy)
}
