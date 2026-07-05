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

	"github.com/lgldsilva/semidx/internal/clientconfig"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

// deps are the runtime dependencies shared by all subcommands, built in the
// root command's PersistentPreRunE and torn down in PersistentPostRun.
//
// The database connection is LAZY: a pure remote client (a machine with no local
// Postgres) must be able to run `login`, `search`, `repo add`, etc. without ever
// dialing a database. Commands that genuinely need local storage call
// d.database(); the connection is opened on first use and cached.
type deps struct {
	cfg      *config.Config
	client   *clientconfig.Config
	emb      embed.Embedder
	db       store.Store
	dbErr    error
	dbOpened bool
	local    *localstore.SQLiteStore
	localErr error
}

// database opens (once) and returns the full PostgreSQL store, or the connection
// error. Used by commands that need the server-only surface (serve, mcp).
func (d *deps) database(ctx context.Context) (store.Store, error) {
	if !d.dbOpened {
		d.dbOpened = true
		db, err := store.NewPgStore(ctx, d.cfg.DatabaseURL)
		if err != nil {
			d.dbErr = fmt.Errorf("connect to database: %w", err)
		}
		d.db = db
	}
	return d.db, d.dbErr
}

// indexStore returns the store used by the index/search path: a standalone local
// SQLite file when local mode is on (SEMIDX_LOCAL_INDEX / --local), otherwise the
// PostgreSQL store. Both satisfy store.IndexStore, so callers stay agnostic.
func (d *deps) indexStore(ctx context.Context) (store.IndexStore, error) {
	if d.cfg.LocalIndexPath != "" {
		if d.local == nil && d.localErr == nil {
			s, err := localstore.New(d.cfg.LocalIndexPath)
			if err != nil {
				d.localErr = fmt.Errorf("open local index %s: %w", d.cfg.LocalIndexPath, err)
			}
			d.local = s
		}
		return d.local, d.localErr
	}
	return d.database(ctx)
}

// remote reports whether a server is configured (remote mode).
func (d *deps) remote() bool { return d.client != nil && d.client.ServerURL != "" }

// apiClient returns an SDK client for the configured server.
func (d *deps) apiClient() *client.Client {
	return client.New(d.client.ServerURL, d.client.Token)
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

// Build metadata, injected at release time via -ldflags -X (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newRootCmd() *cobra.Command {
	// Show commands grouped by workflow (below) instead of one flat alphabetical
	// list, so the natural path index → search is obvious and the destructive
	// commands are visually separated.
	cobra.EnableCommandSorting = false

	d := &deps{}
	var forceLocal, keywordOnly bool
	root := &cobra.Command{
		Use:   "semidx",
		Short: "Self-hosted semantic code search",
		Long: `semidx is self-hosted semantic code search: it chunks your code and docs,
embeds the chunks, and answers natural-language queries with ranked file:line
matches — as a CLI, an HTTP server, or an MCP server for AI agents.

Quickstart (zero-config — no server or API key required):

  semidx index --project .                     # index the current repo
  semidx search --query "where is auth handled"

For better results, configure an embedding provider once:

  semidx config set GEMINI_API_KEY <key>

Run "semidx <command> --help" for details on any command.`,
		Example: `  # Index the current project, then search it
  semidx index --project .
  semidx search --query "rate limiting middleware"

  # Push local files to a server for remote indexing
  semidx push --project . --embed-locally

  # Classic grep-style output (file:line:content)
  semidx sgrep --query "database connection pool"

  # No provider configured? Run fully local, keyword-only
  semidx search --query "auth" --keyword --local`,
		Version:       fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			d.cfg = config.Load()
			// --local forces standalone mode at the default path unless a path was
			// already given via SEMIDX_LOCAL_INDEX.
			if forceLocal && d.cfg.LocalIndexPath == "" {
				d.cfg.LocalIndexPath = config.DefaultLocalIndexPath()
			}
			if keywordOnly {
				d.cfg.KeywordOnly = true
			}
			d.emb = buildChain(d.cfg)
			cc, err := clientconfig.Load()
			if err != nil {
				return fmt.Errorf("load client config: %w", err)
			}
			d.client = cc
			return nil
		},
		PersistentPostRun: func(_ *cobra.Command, _ []string) {
			if d.db != nil {
				d.db.Close()
			}
			if d.local != nil {
				d.local.Close()
			}
		},
	}
	root.PersistentFlags().BoolVar(&forceLocal, "local", false,
		"Use a standalone local index (no server/Postgres); path from SEMIDX_LOCAL_INDEX or the default data dir")
	root.PersistentFlags().BoolVar(&keywordOnly, "keyword", false,
		"Index and search by keyword only, with no embedding model (SEMIDX_EMBED_MODE=none)")
	root.AddGroup(
		&cobra.Group{ID: "primary", Title: "Primary workflow:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
		&cobra.Group{ID: "advanced", Title: "Server & advanced:"},
		&cobra.Group{ID: "maintenance", Title: "Maintenance & danger zone:"},
	)
	addGroup := func(id string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = id
			root.AddCommand(c)
		}
	}
	addGroup("primary", newIndexCmd(d), newPushCmd(d), newSearchCmd(d), newSgrepCmd(d), newUnlockCmd(d))
	addGroup("setup", newConfigCmd(d), newLoginCmd(d), newModelsCmd(d))
	addGroup("advanced", newServeCmd(d), newMCPCmd(d), newRepoCmd(d), newSkillsCmd(d))
	addGroup("maintenance", newMigrateCmd(d), newUpgradeCmd(d), newDropCmd(d))
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
	// Pool mode: when 2+ Ollama URLs are configured, create a ParallelEmbedder
	// with one entry per Ollama instance plus one entry bundling all cloud
	// providers as a fallback chain. This distributes requests across Ollama
	// instances while cloud providers serve as an additional parallel lane.
	if len(cfg.OllamaURLs) >= 2 {
		return buildPool(cfg)
	}

	// Single-URL or no explicit URLs: current chain behaviour (backward compatible).
	var providers []embed.ProviderInstance

	if cfg.GeminiAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "gemini",
			Embedder: embed.NewOpenAIClient(cfg.GeminiBaseURL, cfg.GeminiAPIKey),
			Local:    false,
		})
	}
	if cfg.GroqAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "groq",
			Embedder: embed.NewOpenAIClient(cfg.GroqBaseURL, cfg.GroqAPIKey),
			Local:    false,
		})
	}
	if cfg.OpenRouterAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "openrouter",
			Embedder: embed.NewOpenAIClient(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey),
			Local:    false,
		})
	}
	if cfg.OllamaCloudAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "ollama-cloud",
			Embedder: embed.NewOpenAIClient(cfg.OllamaCloudBaseURL, cfg.OllamaCloudAPIKey),
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

// buildPool creates a ParallelEmbedder when multiple Ollama URLs are configured.
// Each Ollama URL becomes an independent pool entry (local round-robin). Cloud
// providers (Gemini, Groq, OpenRouter, OllamaCloud) are bundled as one
// ChainEmbedder entry so they can still fall through to each other.
func buildPool(cfg *config.Config) embed.Embedder {
	var pool []embed.Embedder

	// One entry per Ollama URL (local or remote).
	for _, url := range cfg.OllamaURLs {
		pool = append(pool, embed.NewOllamaClient(url))
	}

	// Cloud providers bundled as one fallback-chain entry.
	if cloud := buildCloudChain(cfg); cloud != nil {
		pool = append(pool, cloud)
	}

	// Custom provider, if configured, as its own pool entry.
	if custom := customPoolEntry(cfg); custom != nil {
		pool = append(pool, custom)
	}

	return embed.NewParallelEmbedder(pool)
}

// buildCloudChain bundles the configured cloud providers (Gemini, Groq,
// OpenRouter, OllamaCloud) into a single fallback-chain embedder so they can
// fall through to each other. Returns nil when no cloud provider is configured.
func buildCloudChain(cfg *config.Config) embed.Embedder {
	var cloud []embed.ProviderInstance
	if cfg.GeminiAPIKey != "" {
		cloud = append(cloud, embed.ProviderInstance{
			Name: "gemini", Embedder: embed.NewOpenAIClient(
				cfg.GeminiBaseURL, cfg.GeminiAPIKey), Local: false,
		})
	}
	if cfg.GroqAPIKey != "" {
		cloud = append(cloud, embed.ProviderInstance{
			Name: "groq", Embedder: embed.NewOpenAIClient(
				cfg.GroqBaseURL, cfg.GroqAPIKey), Local: false,
		})
	}
	if cfg.OpenRouterAPIKey != "" {
		cloud = append(cloud, embed.ProviderInstance{
			Name: "openrouter", Embedder: embed.NewOpenAIClient(
				cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey), Local: false,
		})
	}
	if cfg.OllamaCloudAPIKey != "" {
		cloud = append(cloud, embed.ProviderInstance{
			Name: "ollama-cloud", Embedder: embed.NewOpenAIClient(
				cfg.OllamaCloudBaseURL, cfg.OllamaCloudAPIKey), Local: false,
		})
	}
	if len(cloud) == 0 {
		return nil
	}
	return embed.NewChainEmbedder(cloud, cfg.Privacy)
}

// customPoolEntry builds the optional custom-provider pool entry from cfg, or
// returns nil when no custom provider is configured.
func customPoolEntry(cfg *config.Config) embed.Embedder {
	if cfg.Provider == "" {
		return nil
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		if cfg.Provider == "ollama" {
			endpoint = cfg.OllamaURLs[0] // use first Ollama URL for custom ollama
		} else {
			endpoint = "https://api.openai.com/v1"
		}
	}
	if cfg.Provider == "openai" {
		return embed.NewOpenAIClient(endpoint, cfg.APIKey)
	}
	return embed.NewOllamaClient(endpoint)
}
