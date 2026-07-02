package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/server"
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

func newIndexCmd(d *deps) *cobra.Command {
	var (
		projectPath, model, gitSince string
		maxFiles                     int
		gitMode, verbose, privacy    bool
	)
	c := &cobra.Command{
		Use:   "index",
		Short: "Index a project directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d.applyPrivacy(privacy)
			if systemDirs[filepath.Clean(projectPath)] {
				return fmt.Errorf("refusing to index system directory: %s", filepath.Clean(projectPath))
			}

			ctx := cmd.Context()
			name := projectNameFromPath(projectPath)
			info, err := d.emb.ModelInfo(ctx, model)
			if err != nil {
				return fmt.Errorf("model info for %s: %w", model, err)
			}
			fmt.Printf("Indexing project: %s\nPath: %s\nModel: %s (dims=%d)\n", name, projectPath, model, info.Dims)

			if err := d.db.EnsureChunksTable(ctx, info.Dims); err != nil {
				return fmt.Errorf("ensure chunks table: %w", err)
			}
			projectID, err := d.db.UpsertProject(ctx, name, projectPath, model)
			if err != nil {
				return fmt.Errorf("upsert project: %w", err)
			}

			indexer := indexing.NewIndexer(d.db, d.emb, info.Dims, d.cfg.IndexWorkers, verbose, gitMode, gitSince)
			start := time.Now()
			stats, err := indexer.IndexProject(ctx, projectID, projectPath, model, maxFiles)
			if err != nil {
				return fmt.Errorf("index project: %w", err)
			}
			fmt.Printf("\nDone in %v\nFiles scanned: %d\nFiles indexed: %d\nFiles skipped (unchanged): %d\nChunks created: %d\nErrors: %d\n",
				time.Since(start), stats.FilesScanned, stats.FilesIndexed, stats.FilesSkipped, stats.ChunksCreated, stats.Errors)
			return nil
		},
	}
	c.Flags().StringVar(&projectPath, "project", "", "Path to project directory")
	c.Flags().StringVar(&model, "model", "bge-m3", "Embedding model name")
	c.Flags().IntVar(&maxFiles, "max-files", 0, "Limit number of files to index (0 = all)")
	c.Flags().BoolVar(&gitMode, "git", false, "Also index git history (git log -p)")
	c.Flags().StringVar(&gitSince, "git-since", "30.days", "git log --since duration (e.g. 7.days)")
	c.Flags().BoolVar(&verbose, "verbose", false, "Show detailed progress and errors")
	c.Flags().BoolVar(&privacy, "privacy", false, "Force local-only providers (Ollama)")
	_ = c.MarkFlagRequired("project")
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			d.applyPrivacy(privacy)
			start := time.Now()
			resp, err := search.NewService(d.db, d.emb).Search(cmd.Context(), search.Request{
				Project: project, Query: query, Model: model, TopK: topK,
			})
			if err != nil {
				return err
			}
			if asJSON {
				return search.JSONFormatter{}.Format(os.Stdout, resp)
			}
			fmt.Printf("Searching project: %s (model: %s)\nQuery: %s\n\n", resp.Project.Name, resp.Model, query)
			if resp.Fallback {
				fmt.Print("[warn] embedding unavailable — used keyword search\n\n")
			}
			fmt.Printf("Found %d results in %v\n\n", len(resp.Results), time.Since(start))
			return search.HumanFormatter{}.Format(os.Stdout, resp)
		},
	}
	addSearchFlags(c, &project, &query, &model, &topK, &privacy, &asJSON)
	return c
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			d.applyPrivacy(privacy)
			resp, err := search.NewService(d.db, d.emb).Search(cmd.Context(), search.Request{
				Project: project, Query: query, Model: model, TopK: topK,
			})
			if err != nil {
				return err
			}
			var f search.Formatter = search.GrepFormatter{ProjectPath: resp.Project.Path}
			if asJSON {
				f = search.JSONFormatter{}
			}
			return f.Format(os.Stdout, resp)
		},
	}
	addSearchFlags(c, &project, &query, &model, &topK, &privacy, &asJSON)
	return c
}

func addSearchFlags(c *cobra.Command, project, query, model *string, topK *int, privacy, asJSON *bool) {
	c.Flags().StringVar(project, "project", "", "Project name")
	c.Flags().StringVar(query, "query", "", "Search query")
	c.Flags().IntVar(topK, "top-k", 5, "Number of results")
	c.Flags().StringVar(model, "model", "", "Override embedding model (default: project model)")
	c.Flags().BoolVar(privacy, "privacy", false, "Force local-only providers (Ollama)")
	c.Flags().BoolVar(asJSON, "json", false, "Output results as JSON")
	_ = c.MarkFlagRequired("project")
	_ = c.MarkFlagRequired("query")
}

func newModelsCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List available embedding models",
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
	return &cobra.Command{
		Use:   "drop",
		Short: "Drop all indexed data",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := d.db.DropAll(cmd.Context()); err != nil {
				return err
			}
			fmt.Println("All indexed data dropped.")
			return nil
		},
	}
}

func newServeCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			srv := server.New(d.db, d.emb, log)

			token, err := srv.EnsureBootstrapToken(cmd.Context(), d.cfg.BootstrapToken)
			if err != nil {
				return fmt.Errorf("bootstrap token: %w", err)
			}
			if token != "" {
				fmt.Fprintf(os.Stderr, "bootstrap admin token (shown once — save it): %s\n", token)
			}
			return srv.Run(cmd.Context(), d.cfg.ListenAddr)
		},
	}
}

func newMCPCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server over stdio (JSON-RPC 2.0)",
		RunE: func(_ *cobra.Command, _ []string) error {
			runMCPServer(d.db, d.emb)
			return nil
		},
	}
}
