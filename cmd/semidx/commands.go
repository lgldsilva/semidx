package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/mcpinstall"
	"github.com/lgldsilva/semidx/internal/mcpserver"
	"github.com/lgldsilva/semidx/internal/pending"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/searchtargets"
	"github.com/lgldsilva/semidx/internal/server"
	"github.com/lgldsilva/semidx/internal/store"
)

// systemDirs must never be indexed (runaway scan / disk blow-up guard).
var systemDirs = map[string]bool{
	"/": true, "/home": true, "/etc": true, "/usr": true, "/var": true,
	"/opt": true, "/bin": true, "/sbin": true, "/lib": true,
}

func (d *deps) applyPrivacy(force bool) {
	if ce, ok := d.emb.(*embed.ChainEmbedder); ok {
		ce.SetPrivacy(force || d.cfg.Privacy)
	}
}

// modelDims resolves the embedding dimensions for a model: the fixed keyword
// bucket in keyword-only mode, otherwise the provider's reported dims. Callers
// wrap the error with their own context.
func (d *deps) modelDims(ctx context.Context, model string) (int, error) {
	if d.keywordOnly {
		return store.KeywordDims, nil
	}
	info, err := d.emb.ModelInfo(ctx, model)
	if err != nil {
		return 0, err
	}
	return info.Dims, nil
}

// indexTarget is how a --project path resolves into the store's identity model:
// a git project (keyed by repo identity, rooted at the worktree toplevel) or a
// document folder (keyed by its absolute path). Shared by index and unlock so
// both agree on the same project.
type indexTarget struct {
	indexPath  string // walk root (repo toplevel for git; the path for docs)
	worktree   string // current worktree toplevel for git; "" for docs
	identity   string // stable key: git identity, or "path:<abs>" for docs
	sourceType string // "git" | "docs"
	name       string // display name (repo/dir basename)
}

func resolveTarget(ctx context.Context, projectPath string, docs bool) indexTarget {
	gi := gitmeta.Resolve(ctx, projectPath)
	if gi.IsGit && !docs {
		return indexTarget{gi.Toplevel, gi.Toplevel, gi.Identity, "git", projectNameFromPath(gi.Toplevel)}
	}
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		abs = projectPath
	}
	return indexTarget{projectPath, "", "path:" + abs, "docs", projectNameFromPath(projectPath)}
}

// applyBranchSuffix suffixes the identity with #<branch> and the display name
// with @<branch> so a git project indexed with --branch gets its own project
// row. For non-git (docs) projects the branch flag is silently ignored.
func applyBranchSuffix(tgt indexTarget, branch string) indexTarget {
	if branch != "" && tgt.sourceType == "git" {
		tgt.identity = tgt.identity + "#" + branch
		tgt.name = tgt.name + "@" + branch
	}
	return tgt
}

// docsFlagHint echoes " --docs" when the docs flag was used, so a printed
// unlock hint reproduces the same invocation.
func docsFlagHint(docs bool) string {
	if docs {
		return " --docs"
	}
	return ""
}

// printIndexHeader prints the pre-index banner: the model and its dims, or the
// keyword-only notice when no embeddings are used.
func printIndexHeader(tgt indexTarget, model string, dims int, keywordOnly bool) {
	if keywordOnly {
		fmt.Printf("Indexing project: %s\nPath: %s (%s)\nMode: keyword-only (no embeddings)\n", tgt.name, tgt.indexPath, tgt.sourceType)
		return
	}
	fmt.Printf("Indexing project: %s\nPath: %s (%s)\nModel: %s (dims=%d)\n", tgt.name, tgt.indexPath, tgt.sourceType, model, dims)
}

// recordEncryptedPending saves the password-protected files that `index`
// skipped so `semidx unlock` can find them later, and points the user at it. A
// no-op when nothing was encrypted.
func recordEncryptedPending(tgt indexTarget, model, projectPath string, docs bool, stats *indexing.IndexStats) {
	if stats.FilesEncrypted == 0 {
		return
	}
	abs := make([]string, 0, len(stats.EncryptedPaths))
	for _, rel := range stats.EncryptedPaths {
		abs = append(abs, filepath.Join(tgt.indexPath, rel))
	}
	if err := pending.Save(tgt.identity, &pending.Registry{Project: tgt.name, Model: model, Files: abs}); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] could not record encrypted files: %v\n", err)
	}
	fmt.Printf("Files needing a password: %d\n  → run: semidx unlock --project %s%s\n",
		stats.FilesEncrypted, projectPath, docsFlagHint(docs))
}

func newIndexCmd(d *deps) *cobra.Command {
	var (
		projectPath, model, gitSince, branch string
		maxFiles                             int
		gitMode, verbose, privacy            bool
		docs                                 bool
	)
	c := &cobra.Command{
		Use:   "index",
		Short: "Index a project directory",
		Long: `Index a project so it can be searched. A git repo becomes one logical index
keyed by its identity (shared across worktrees/clones); any other folder — or
one passed with --docs — is keyed by its absolute path. Oversized and
password-protected files are skipped; run "semidx unlock" for the latter.

With --branch <name>, the project is registered as a separate index entry with
"name@branch" as the display name and "#branch" appended to the identity, so
each branch lives in its own project row. This works with any checkout — no
git checkout is performed; the currently checked-out content is indexed under
the branch label.

With no embedding provider configured, add --keyword to index text-only.`,
		Example: `  semidx index --project .                 # index the current repo
  semidx index --project ./docs --docs     # a plain document folder
  semidx index --project . --git           # also index recent git history
  semidx index --project . --keyword       # no embeddings, keyword-only
  semidx index --project . --branch develop  # index as "repo@develop"`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d.applyPrivacy(privacy)
			if systemDirs[filepath.Clean(projectPath)] {
				return fmt.Errorf("refusing to index system directory: %s", filepath.Clean(projectPath))
			}

			ctx := cmd.Context()
			db, err := d.indexStore(ctx)
			if err != nil {
				return err
			}

			// A git project is one logical index keyed by repo identity; a non-git
			// dir (or --docs) is a document folder keyed by its absolute path.
			tgt := resolveTarget(ctx, projectPath, docs)

			// When --branch is set, suffix both the identity and display name so the
			// branch gets its own project row (separate from the main branch).
			tgt = applyBranchSuffix(tgt, branch)

			// Keyword-only mode needs no model: dims come from the fixed text bucket.
			dims, err := d.modelDims(ctx, model)
			if err != nil {
				return fmt.Errorf("model info for %s: %w (no embedding provider reachable? re-run with --keyword to index text-only)", model, err)
			}
			printIndexHeader(tgt, model, dims, d.keywordOnly)

			if err := db.EnsureChunksTable(ctx, dims); err != nil {
				return fmt.Errorf("ensure chunks table: %w", err)
			}
			projectID, err := db.EnsureProjectIdentity(ctx, tgt.identity, tgt.name, tgt.indexPath, model, tgt.sourceType, dims)
			if err != nil {
				return fmt.Errorf("register project: %w", err)
			}

			indexer := indexing.NewIndexer(db, d.emb, dims, indexing.IndexerOpts{
				Workers:             d.cfg.IndexWorkers,
				EmbedBatchSize:      d.cfg.EmbedBatchSize,
				MaxFileSize:         d.cfg.MaxFileSize,
				MaxChunksPerFile:    d.cfg.MaxChunksPerFile,
				MaxChunksPerProject: d.cfg.MaxChunksPerProject,
				Verbose:             verbose,
				GitMode:             gitMode,
				GitSince:            gitSince,
			}).
				SetKeywordOnly(d.keywordOnly).
				SetWorktree(tgt.worktree)
			start := time.Now()
			stats, err := indexer.IndexProject(ctx, projectID, tgt.indexPath, model, maxFiles)
			if err != nil {
				return fmt.Errorf("index project: %w", err)
			}
			fmt.Printf("\nDone in %v\nFiles scanned: %d\nFiles indexed: %d\nFiles skipped (unchanged): %d\nChunks created: %d\nErrors: %d\n",
				time.Since(start), stats.FilesScanned, stats.FilesIndexed, stats.FilesSkipped, stats.ChunksCreated, stats.Errors)

			// Record password-protected files so `semidx unlock` can find them, and
			// point the user at it.
			recordEncryptedPending(tgt, model, projectPath, docs, stats)
			return nil
		},
	}
	c.Flags().StringVar(&projectPath, "project", ".", "Path to the project directory (default: current directory)")
	c.Flags().StringVar(&model, "model", "bge-m3", "Embedding model name")
	c.Flags().IntVar(&maxFiles, "max-files", 0, "Limit number of files to index (0 = all)")
	c.Flags().BoolVar(&docs, "docs", false, "Treat the path as a document folder (absolute-path identity), even inside a git repo")
	c.Flags().BoolVar(&gitMode, "git", false, "Also index git history (git log -p)")
	c.Flags().StringVar(&gitSince, "git-since", "30.days", "git log --since duration (e.g. 7.days)")
	c.Flags().StringVar(&branch, "branch", "", "Index as a separate project for this branch (suffixes identity and display name; no git checkout performed)")
	c.Flags().BoolVar(&verbose, "verbose", false, "Show detailed progress and errors")
	c.Flags().BoolVar(&privacy, "privacy", false, "Force local-only providers (Ollama)")
	return c
}

func newSearchCmd(d *deps) *cobra.Command {
	var (
		project, query, model string
		topK                  int
		privacy, asJSON       bool
	)
	c := &cobra.Command{
		Use:   "search",
		Short: "Semantic search over an indexed project",
		Long: `Search an indexed project with a natural-language query and get ranked
file:line matches. With no --project, semidx searches the project enclosing the
current directory, falling back to every indexed project (labeled per project).
When embeddings are unavailable it transparently falls back to keyword search.`,
		Example: `  semidx search --query "where is auth handled"
  semidx search --query "retry with backoff" --top-k 10
  semidx search --project ./my-repo --query "http timeout"
  semidx search --query "auth" --json        # machine-readable output`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			results, err := d.runSearchTargets(cmd, project, query, model, topK, privacy)
			if err != nil {
				return err
			}
			if asJSON {
				return renderSearchJSON(os.Stdout, results)
			}
			return renderSearchResults(query, results)
		},
	}
	addSearchFlags(c, &project, &query, &model, &topK, &privacy, &asJSON)
	return c
}

// renderSearchResults prints human-readable search results, tagging each block
// with its project name when more than one project was searched.
func renderSearchResults(query string, results []projSearch) error {
	multi := len(results) > 1
	if multi {
		fmt.Printf("Query: %s (searching %d projects)\n\n", query, len(results))
	}
	for _, ps := range results {
		if err := renderProjectSearch(query, ps, multi); err != nil {
			return err
		}
	}
	return nil
}

// renderProjectSearch prints one project's header and its formatted matches.
func renderProjectSearch(query string, ps projSearch, multi bool) error {
	if multi {
		fmt.Printf("=== project: %s ===\n", ps.name)
	} else {
		fmt.Printf("Searching project: %s (model: %s)\nQuery: %s\n\n", ps.resp.Project.Name, ps.resp.Model, query)
	}
	if ps.resp.Fallback {
		fmt.Fprint(os.Stderr, "[warn] embedding unavailable — used keyword search\n\n")
	}
	fmt.Printf("Found %d results in %v\n\n", len(ps.resp.Results), ps.took)
	return (search.HumanFormatter{}).Format(os.Stdout, ps.resp)
}

func newSgrepCmd(d *deps) *cobra.Command {
	var (
		project, query, model string
		topK                  int
		privacy, asJSON       bool
	)
	c := &cobra.Command{
		Use:   "sgrep",
		Short: "Semantic search with classic grep output (file:line:content)",
		Long: `Like "search", but prints classic grep-style file:line:content (one line per
match) so results pipe cleanly into editors and scripts. Paths are absolute so
they stay correct even when searching across multiple projects.`,
		Example: `  semidx sgrep --query "database connection pool"
  semidx sgrep --query "TODO" --project ./my-repo
  semidx sgrep --query "auth middleware" | fzf`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			results, err := d.runSearchTargets(cmd, project, query, model, topK, privacy)
			if err != nil {
				return err
			}
			if asJSON {
				return renderSearchJSON(os.Stdout, results)
			}
			// Single project: keep the exact classic anchoring (protects the sgrep
			// golden).
			if len(results) == 1 {
				resp := results[0].resp
				path := d.sgrepProjectPath(cmd.Context(), resp.Project.Path)
				return search.GrepFormatter{ProjectPath: path}.Format(os.Stdout, resp)
			}
			return renderSgrepMulti(results)
		},
	}
	addSearchFlags(c, &project, &query, &model, &topK, &privacy, &asJSON)
	return c
}

// sgrepProjectPath picks the path grep output is anchored at for a single
// project: remote → the current working directory; a local git project → the
// current worktree root; otherwise the stored project path.
func (d *deps) sgrepProjectPath(ctx context.Context, stored string) string {
	if d.remote() {
		if wd, e := os.Getwd(); e == nil {
			return wd
		}
		return stored
	}
	if wt := currentWorktreeRoot(ctx); wt != "" {
		return wt
	}
	return stored
}

// renderSgrepMulti prints grep-style output across projects, anchoring each
// result at its own project's path so the absolute file:line stays correct and
// shows which project it came from.
func renderSgrepMulti(results []projSearch) error {
	for _, ps := range results {
		if err := (search.GrepFormatter{ProjectPath: ps.resp.Project.Path}).Format(os.Stdout, ps.resp); err != nil {
			return err
		}
	}
	return nil
}

func addSearchFlags(c *cobra.Command, project, query, model *string, topK *int, privacy, asJSON *bool) {
	c.Flags().StringVar(project, "project", "", "Project path or name (default: the project enclosing the current directory, else all)")
	c.Flags().StringVar(query, "query", "", "Search query")
	c.Flags().IntVar(topK, "top-k", 5, "Number of results")
	c.Flags().StringVar(model, "model", "", "Override embedding model (default: project model)")
	c.Flags().BoolVar(privacy, "privacy", false, "Force local-only providers (Ollama)")
	c.Flags().BoolVar(asJSON, "json", false, "Output results as JSON")
	c.Flags().Bool("graph", false, "Expand results via dependency graph (Graph-RAG)")
	c.Flags().Int("graph-depth", 2, "Max BFS depth for graph expansion")
	_ = c.MarkFlagRequired("query")
}

func newModelsCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List available embedding models",
		Long: `List the embedding models advertised by the configured providers (the chain
Gemini → Groq → OpenRouter → Ollama Cloud → local Ollama).`,
		Example: "  semidx models",
		RunE: func(cmd *cobra.Command, _ []string) error {
			models, err := d.emb.ListModels(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Println("Available embedding models:")
			for _, m := range models {
				fmt.Printf("  - %s\n", m)
			}
			return nil
		},
	}
}

func newDropCmd(d *deps) *cobra.Command {
	var confirm bool
	c := &cobra.Command{
		Use:   "drop",
		Short: "Drop all indexed data",
		Long: `Delete ALL indexed data — every project, file and chunk — from the active
store. This is destructive and cannot be undone. You must confirm: either type
"yes" at the interactive prompt, or pass --confirm (e.g. in scripts).`,
		Example: "  semidx drop --confirm",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !confirm {
				fmt.Fprint(os.Stderr, "This permanently deletes ALL indexed data. Type 'yes' to continue (or pass --confirm): ")
				var answer string
				// A non-interactive stdin (pipe/CI with no input) yields EOF here, so
				// answer stays empty and the drop is safely aborted.
				_, _ = fmt.Scanln(&answer)
				if strings.ToLower(strings.TrimSpace(answer)) != "yes" {
					fmt.Fprintln(os.Stderr, "Aborted.")
					return nil
				}
			}
			db, err := d.indexStore(cmd.Context())
			if err != nil {
				return err
			}
			if err := db.DropAll(cmd.Context()); err != nil {
				return err
			}
			fmt.Println("All indexed data dropped.")
			return nil
		},
	}
	c.Flags().BoolVar(&confirm, "confirm", false, "Skip the interactive prompt and drop immediately (for scripts)")
	return c
}

func newStatusCmd(d *deps) *cobra.Command {
	var projectPath string
	c := &cobra.Command{
		Use:   "status",
		Short: "Show indexing status of a project",
		Long: `Show the indexing status of a project: whether it is ready, how many files
are indexed, and what embedding model is in use.`,
		Example: `  semidx status                          # show status for the current project
  semidx status --project ./my-repo      # show status for a specific project`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if d.remote() {
				// Remote mode: resolve project ref via server listing, then get status.
				api := d.apiClient()
				p, err := searchtargets.ResolveRemoteProject(ctx, api, projectPath)
				if err != nil {
					return err
				}
				resp, err := api.Status(ctx, p.Name)
				if err != nil {
					return err
				}
				fmt.Printf("Project: %s\n", resp.Name)
				if resp.Identity != "" {
					fmt.Printf("Identity: %s\n", resp.Identity)
				}
				fmt.Printf("Source: %s\n", resp.SourceType)
				fmt.Printf("Backend: remote (%s)\n", d.client.ServerURL)
				fmt.Printf("Status: %s\n", resp.Status)
				if resp.Model != "" {
					fmt.Printf("Model: %s\n", resp.Model)
				}
				fmt.Printf("Total indexed: %d files\n", resp.TotalFiles)
				fmt.Println("Run `semidx push` to check for stale files.")
				return nil
			}

			// Local mode: resolve via identity, query the local store.
			tgt := resolveTarget(ctx, projectPath, false)
			db, err := d.indexStore(ctx)
			if err != nil {
				return err
			}

			// Try identity first (git or path:), then name fallback.
			proj, err := db.GetProjectByIdentity(ctx, tgt.identity)
			if err != nil {
				// Fallback: try by name (basename).
				proj, err = db.GetProject(ctx, tgt.name)
				if err != nil {
					return fmt.Errorf("project not found — index it first with `semidx index --project %s`", projectPath)
				}
			}

			hashes, err := db.ListFileHashes(ctx, proj.ID)
			if err != nil {
				return fmt.Errorf("list files: %w", err)
			}

			fmt.Printf("Project: %s\n", proj.Name)
			if proj.Identity != "" {
				fmt.Printf("Identity: %s\n", proj.Identity)
			}
			fmt.Printf("Source: %s\n", proj.SourceType)
			fmt.Printf("Backend: local\n")
			fmt.Printf("Status: %s\n", proj.Status)
			if proj.Model != "" {
				fmt.Printf("Model: %s\n", proj.Model)
			}
			fmt.Printf("Total indexed: %d files\n", len(hashes))
			fmt.Println("Run `semidx index` to reindex.")
			return nil
		},
	}
	c.Flags().StringVar(&projectPath, "project", ".", "Path to the project directory (default: current directory)")
	return c
}

func newServeCmd(d *deps) *cobra.Command {
	var showBootstrapToken bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API server",
		Long: `Run the semidx HTTP API server (and the embedded web admin at /admin). On
first run it generates a one-time bootstrap admin token. Requires Postgres
(SEMIDX_DB_DSN); listens on SEMIDX_LISTEN_ADDR.`,
		Example: `  semidx serve
  SEMIDX_LISTEN_ADDR=:8080 semidx serve`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			db, err := d.database(cmd.Context())
			if err != nil {
				return err
			}
			log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			srv := server.New(db, d.emb, log)
			srv.SetGitAllowFile(d.cfg.GitAllowFile)
			srv.SetMetricsToken(d.cfg.MetricsToken)

			if err := d.bootstrapServer(cmd.Context(), srv, showBootstrapToken); err != nil {
				return err
			}
			srv.StartWorkers(cmd.Context(), d.cfg.IndexWorkers, d.cfg.DataDir)
			return srv.Run(cmd.Context(), d.cfg.ListenAddr)
		},
	}
	cmd.Flags().BoolVar(&showBootstrapToken, "show-bootstrap-token", false, "Display the one-time bootstrap admin token (generated on first run)")
	return cmd
}

// bootstrapServer runs the one-time server setup: bootstrap admin token + user,
// optional JWT control tokens, and mounting the web admin UI. The bootstrap
// token is written to a file inside DataDir (bootstrap-token.txt) so it never
// leaks to stderr/systemd/journald; pass showBootstrapToken=true to also print
// it to stderr for interactive use.
func (d *deps) bootstrapServer(ctx context.Context, srv *server.Server, showBootstrapToken bool) error {
	token, err := srv.EnsureBootstrapToken(ctx, d.cfg.BootstrapToken)
	if err != nil {
		return fmt.Errorf("bootstrap token: %w", err)
	}
	if token != "" {
		// Write to a well-known file with restricted permissions, never to stderr
		// so it doesn't leak into systemd/journald logs.
		tokenFile := filepath.Join(d.cfg.DataDir, "bootstrap-token.txt")
		if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
			return fmt.Errorf("bootstrap token dir: %w", err)
		}
		if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
			return fmt.Errorf("bootstrap token file: %w", err)
		}
		if showBootstrapToken {
			fmt.Fprintf(os.Stderr, "bootstrap admin token (shown once — save it): %s\n", token)
		}
		fmt.Fprintf(os.Stderr, "bootstrap admin token saved to %s (use --show-bootstrap-token to display it)\n", tokenFile)
	}
	admin, err := srv.EnsureBootstrapAdmin(ctx, d.cfg.BootstrapAdminUser, d.cfg.BootstrapAdminPassword)
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if admin != "" {
		fmt.Fprintf(os.Stderr, "bootstrap web admin user created: %s\n", admin)
	}
	if d.cfg.JWTSecret != "" {
		if err := srv.EnableJWT(d.cfg.JWTSecret); err != nil {
			return fmt.Errorf("enable JWT control tokens: %w", err)
		}
	}
	if err := srv.MountAdmin(d.cfg.CookieSecure, d.cfg.CSRFKey); err != nil {
		return fmt.Errorf("mount admin UI: %w", err)
	}
	return nil
}

func newMCPCmd(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server over stdio (proxying to a server, or over the local index)",
		Long: `Run the Model Context Protocol server over stdio so AI agents can call
semantic_search / semantic_projects / semantic_reindex. Proxies a configured
server, or serves the local index directly. Use "semidx mcp install" to wire it
into an agent client. (stdout carries the protocol — logs go to stderr.)`,
		Example: `  semidx mcp                                  # run the stdio server
  semidx mcp install --client claude-code --apply`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			// Remote mode when a server is configured; otherwise serve the standalone
			// local index directly, so MCP works without a server (like the CLI).
			if d.remote() {
				return mcpserver.Run(ctx, mcpserver.NewClientBackend(d.apiClient()))
			}
			db, err := d.indexStore(ctx)
			if err != nil {
				return err
			}
			backend := mcpserver.NewLocalBackend(search.NewService(db, d.emb), db, d.keywordOnly)
			return mcpserver.Run(ctx, backend)
		},
	}
	cmd.AddCommand(newMCPInstallCmd())
	return cmd
}

// newMCPInstallCmd registers semidx's stdio MCP server in an agent's config,
// modeled on ai-memory's `install-mcp`: print a snippet by default, or --apply
// it idempotently (backup + replace the named entry, preserving the rest).
func newMCPInstallCmd() *cobra.Command {
	var (
		clientID   string
		name       string
		apply      bool
		configFile string
		list       bool
		all        bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Register the semidx MCP server in an agent's config (Claude Code, Cursor, Gemini CLI, VS Code, OpenCode, Codex)",
		Long: "Register semidx's stdio MCP server in a coding agent's configuration.\n\n" +
			"By default it PRINTS the config snippet and its target path; pass --apply to\n" +
			"merge it in place (a timestamped .bak is written first and other servers are\n" +
			"preserved). Pass --all to apply to every supported client at once.\n\n" +
			"Supported clients:\n\n" + mcpinstall.ClientList(),
		Example: "  semidx mcp install --list\n  semidx mcp install --client claude-code --apply\n  semidx mcp install --all --apply",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if list {
				fmt.Print(mcpinstall.ClientList())
				return nil
			}
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve semidx path: %w", err)
			}
			if abs, aerr := filepath.Abs(exe); aerr == nil {
				exe = abs
			}
			home, _ := os.UserHomeDir()
			configDir, _ := os.UserConfigDir()
			project, _ := os.Getwd()
			if all {
				return installAll(exe, home, configDir, project, name, apply)
			}
			opts := mcpinstall.Options{
				Client:    clientID,
				Name:      name,
				ExePath:   exe,
				Home:      home,
				ConfigDir: configDir,
				Project:   project,
				FilePath:  configFile,
			}
			path, snippet, err := mcpinstall.Snippet(opts)
			if err != nil {
				return err
			}
			if !apply {
				fmt.Printf("# %s — add to %s:\n\n%s\n", clientID, path, snippet)
				fmt.Print("Re-run with --apply to write this automatically.\n")
				return nil
			}
			written, err := mcpinstall.Apply(opts)
			if err != nil {
				return err
			}
			fmt.Printf("Registered MCP server %q for %s in %s\n", name, clientID, written)
			return nil
		},
	}
	cmd.Flags().StringVar(&clientID, "client", "claude-code", "target agent client (see --list)")
	cmd.Flags().StringVar(&name, "name", "semidx", "server entry name in the client config")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the config in place (default: print the snippet)")
	cmd.Flags().StringVar(&configFile, "config-file", "", "override the client's config file path")
	cmd.Flags().BoolVar(&list, "list", false, "list supported clients and exit")
	cmd.Flags().BoolVar(&all, "all", false, "apply to every supported client at once (implies --apply)")
	return cmd
}

// installAll applies the MCP server entry to every applyable client.
func installAll(exe, home, configDir, project, name string, apply bool) error {
	clients := mcpinstall.ApplyableClients()
	for _, c := range clients {
		opts := mcpinstall.Options{
			Client:    c.ID,
			Name:      name,
			ExePath:   exe,
			Home:      home,
			ConfigDir: configDir,
			Project:   project,
		}
		if apply {
			written, err := mcpinstall.Apply(opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "skipping %s: %v\n", c.ID, err)
				continue
			}
			fmt.Printf("Registered MCP server %q for %s in %s\n", name, c.ID, written)
		} else {
			path, snippet, err := mcpinstall.Snippet(opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "skipping %s: %v\n", c.ID, err)
				continue
			}
			fmt.Printf("# %s — add to %s:\n\n%s\n", c.ID, path, snippet)
		}
	}
	return nil
}
