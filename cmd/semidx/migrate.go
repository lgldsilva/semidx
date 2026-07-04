package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/store"
)

// newMigrateCmd copies a standalone SQLite index into Postgres WITHOUT
// re-embedding: the stored vectors are read from SQLite and re-inserted, so a
// laptop's local index becomes the server's without re-running any model.
func newMigrateCmd(d *deps) *cobra.Command {
	var fromPath, toDSN string
	c := &cobra.Command{
		Use:   "migrate",
		Short: "Copy a local SQLite index into Postgres without re-embedding",
		Long: "Copy every project, file and chunk (with its embedding) from a local SQLite\n" +
			"index into a PostgreSQL/pgvector database — no re-indexing, no re-embedding.\n\n" +
			"  --from  source SQLite path (default: the configured local index)\n" +
			"  --to    target Postgres DSN (default: SEMIDX_DB_DSN)\n\n" +
			"Projects are matched by identity, so re-running is idempotent per (project,\n" +
			"file, dims). Worktree manifests are not copied — re-run `semidx index` in a\n" +
			"git worktree if you need worktree-scoped search there.",
		Example: "  semidx migrate\n  semidx migrate --from ./local.db --to postgres://localhost:5432/semidx",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			if fromPath == "" {
				fromPath = d.cfg.LocalIndexPath
			}
			if fromPath == "" {
				fromPath = config.DefaultLocalIndexPath()
			}
			src, err := localstore.New(fromPath)
			if err != nil {
				return fmt.Errorf("open source SQLite index %s: %w", fromPath, err)
			}
			defer src.Close()

			if toDSN == "" {
				toDSN = d.cfg.DatabaseURL
			}
			pg, err := store.NewPgStore(ctx, toDSN)
			if err != nil {
				return fmt.Errorf("connect to target Postgres: %w", err)
			}
			defer pg.Close()

			projects, err := src.ListProjects(ctx)
			if err != nil {
				return fmt.Errorf("list source projects: %w", err)
			}
			if len(projects) == 0 {
				fmt.Printf("No projects in %s — nothing to migrate.\n", fromPath)
				return nil
			}
			fmt.Printf("Migrating %d project(s) from %s → Postgres\n", len(projects), fromPath)

			var totalChunks, totalFiles int
			for _, p := range projects {
				identity := p.Identity
				if identity == "" {
					identity = "path:" + p.Path
				}
				pid, err := pg.EnsureProjectIdentity(ctx, identity, p.Name, p.Path, p.Model, p.SourceType)
				if err != nil {
					return fmt.Errorf("register project %q: %w", p.Name, err)
				}
				rows, err := src.ExportChunks(ctx, p.ID)
				if err != nil {
					return fmt.Errorf("export %q: %w", p.Name, err)
				}
				files, chunks, err := migrateRows(ctx, pg, pid, rows)
				if err != nil {
					return fmt.Errorf("migrate %q: %w", p.Name, err)
				}
				totalFiles += files
				totalChunks += chunks
				fmt.Printf("  %-28s %d files, %d chunks\n", p.Name, files, chunks)
			}
			fmt.Printf("Done: %d files, %d chunks migrated (no re-embedding).\n", totalFiles, totalChunks)
			return nil
		},
	}
	c.Flags().StringVar(&fromPath, "from", "", "source SQLite index path (default: the local index)")
	c.Flags().StringVar(&toDSN, "to", "", "target Postgres DSN (default: SEMIDX_DB_DSN)")
	return c
}

// migrateRows inserts exported chunks into the target store, grouped by (file,
// dims) — each group becomes one UpsertFile + InsertChunks (or, when the chunks
// were stored text-only, InsertChunksTextOnly). Returns the file and chunk counts.
func migrateRows(ctx context.Context, pg store.IndexStore, projectID int, rows []localstore.ExportedChunk) (files, chunks int, err error) {
	ensured := map[int]bool{}
	i := 0
	for i < len(rows) {
		path, dims := rows[i].FilePath, rows[i].Dims
		var group []localstore.ExportedChunk
		for i < len(rows) && rows[i].FilePath == path && rows[i].Dims == dims {
			group = append(group, rows[i])
			i++
		}
		if !ensured[dims] {
			if err := pg.EnsureChunksTable(ctx, dims); err != nil {
				return files, chunks, err
			}
			ensured[dims] = true
		}
		fileID, err := pg.UpsertFile(ctx, projectID, path, group[0].FileHash, group[0].FileSize)
		if err != nil {
			return files, chunks, err
		}
		cks := make([]chunker.Chunk, len(group))
		embs := make([][]float32, len(group))
		textOnly := false
		for j, g := range group {
			cks[j] = chunker.Chunk{Content: g.Content, StartLine: g.StartLine, EndLine: g.EndLine}
			embs[j] = g.Embedding
			if g.Embedding == nil {
				textOnly = true
			}
		}
		if textOnly {
			err = pg.InsertChunksTextOnly(ctx, projectID, fileID, cks, dims)
		} else {
			err = pg.InsertChunks(ctx, projectID, fileID, cks, embs, dims)
		}
		if err != nil {
			return files, chunks, err
		}
		files++
		chunks += len(group)
	}
	return files, chunks, nil
}
