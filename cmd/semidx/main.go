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

	// localIndexPath is the resolved local SQLite path: SEMIDX_LOCAL_INDEX, or
	// the default data dir when --local is used. It is computed in
	// PersistentPreRunE so the loaded config is never mutated.
	localIndexPath string
	// keywordOnly is the resolved keyword-only flag: SEMIDX_EMBED_MODE=none or
	// --keyword. It is computed in PersistentPreRunE so the loaded config is
	// never mutated.
	keywordOnly bool
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
	if d.localIndexPath != "" {
		if d.local == nil && d.localErr == nil {
			s, err := localstore.New(d.localIndexPath)
			if err != nil {
				d.localErr = fmt.Errorf("open local index %s: %w", d.localIndexPath, err)
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
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return d.setup(cmd, forceLocal, keywordOnly)
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
	addGroup("primary", newIndexCmd(d), newPushCmd(d), newSearchCmd(d), newSgrepCmd(d), newUnlockCmd(d), newStatusCmd(d), newDeadCodeCmd(d), newCallersCmd(d), newExplainCmd(d))
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
