// Command chatrag is an interactive RAG (Retrieval-Augmented Generation) chat
// over a semidx local index. It retrieves relevant code/document chunks and
// answers natural-language questions about your project.
//
// chatrag is local-only — it always uses the SQLite local store (no remote
// server, no PostgreSQL). All config is read from environment / .env / the
// semidx user config file via the same config.Load() used by the parent semidx
// CLI.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/search"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		localIndex string
		project    string
		model      string
		bindAddr   string
	)

	root := &cobra.Command{
		Use:   "chatrag",
		Short: "Semantic chat over your codebase via RAG",
		Long: `chatrag is an interactive RAG (Retrieval-Augmented Generation) chat
over a semidx local index. It retrieves relevant code/document chunks and
answers natural-language questions about your project.

Requires a chat model API key (e.g. GEMINI_API_KEY) and an indexed project:

  export GEMINI_API_KEY=...
  semidx index --project .
  chatrag --project .

Commands:
  chatrag [flags]            Start the interactive chat REPL
  chatrag serve              Start the HTTP server with web chat UI`,
		Example: `  chatrag --project .                              # Chat against the current directory's index
  chatrag --project myproject --model gemini-2.5-flash
  chatrag --local ~/custom/index.db --project .
  chatrag serve --project . --bind :8976          # Start the web chat UI`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       "dev",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd.Context(), localIndex, project, model)
		},
	}

	root.PersistentFlags().StringVar(&localIndex, "local", defaultLocalIndexPath(),
		"SQLite index path (default: "+defaultLocalIndexPath()+")")
	root.PersistentFlags().StringVarP(&project, "project", "p", "",
		"Project name or path to search")
	root.PersistentFlags().StringVarP(&model, "model", "m", defaultChatModel,
		"Chat model (default: "+defaultChatModel+")")
	root.PersistentFlags().StringVar(&bindAddr, "bind", "127.0.0.1:8976",
		"HTTP server listen address (used by 'serve' subcommand; default binds localhost only)")

	// Hidden chat subcommand so `chatrag chat` also works.
	root.AddCommand(&cobra.Command{
		Use:    "chat",
		Short:  "Start the interactive chat REPL (default)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd.Context(), localIndex, project, model)
		},
	})

	root.AddCommand(newServeCmd())

	return root
}

// ---------------------------------------------------------------------------
// Thin wrappers so runChat doesn't import config/embed/localstore/search
// directly for one-liner operations.
// ---------------------------------------------------------------------------

func defaultLocalIndexPath() string {
	return config.DefaultLocalIndexPath()
}

func openLocalStore(path string) (*localstore.SQLiteStore, error) {
	return localstore.New(path)
}

func newEmbedder(cfg *config.Config) embed.Embedder {
	return embed.NewChainFromConfig(embed.ChainConfig{
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
	})
}

func newSearchService(s *localstore.SQLiteStore, emb embed.Embedder) *search.Service {
	return search.NewService(s, emb)
}
