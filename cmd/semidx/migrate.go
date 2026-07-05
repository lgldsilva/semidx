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

			fromPath = d.resolveMigrateSource(fromPath)
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

			projects, err := src.ListProjects(ctx, 0, 0)
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
				files, chunks, err := migrateProject(ctx, src, pg, p)
				if err != nil {
					return err
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

// resolveMigrateSource resolves the source SQLite path: an explicit --from, else
// the configured local index, else the built-in default.
func (d *deps) resolveMigrateSource(fromPath string) string {
	if fromPath != "" {
		return fromPath
	}
	if d.cfg.LocalIndexPath != "" {
		return d.cfg.LocalIndexPath
	}
	return config.DefaultLocalIndexPath()
}

// migrateProject copies one project (identity, then all its chunks) into the
// target store and returns the migrated file and chunk counts.
func migrateProject(ctx context.Context, src *localstore.SQLiteStore, pg *store.PgStore, p store.Project) (files, chunks int, err error) {
	identity := p.Identity
	if identity == "" {
		identity = "path:" + p.Path
	}
	pid, err := pg.EnsureProjectIdentity(ctx, identity, p.Name, p.Path, p.Model, p.SourceType, p.Dims)
	if err != nil {
		return 0, 0, fmt.Errorf("register project %q: %w", p.Name, err)
	}
	rows, err := src.ExportChunks(ctx, p.ID)
	if err != nil {
		return 0, 0, fmt.Errorf("export %q: %w", p.Name, err)
	}
	files, chunks, err = migrateRows(ctx, pg, pid, rows)
	if err != nil {
		return 0, 0, fmt.Errorf("migrate %q: %w", p.Name, err)
	}
	return files, chunks, nil
}

// migrateRows inserts exported chunks into the target store, grouped by (file,
// dims) — each group becomes one UpsertFile + InsertChunks (or, when the chunks
// were stored text-only, InsertChunksTextOnly). Returns the file and chunk counts.
func migrateRows(ctx context.Context, pg store.IndexStore, projectID int, rows []localstore.ExportedChunk) (files, chunks int, err error) {
	ensured := map[int]bool{}
	for i := 0; i < len(rows); {
		var group []localstore.ExportedChunk
		group, i = nextFileGroup(rows, i)
		dims := group[0].Dims
		if err := ensureChunksTableOnce(ctx, pg, ensured, dims); err != nil {
			return files, chunks, err
		}
		fileID, err := pg.UpsertFile(ctx, projectID, group[0].FilePath, group[0].FileHash, group[0].FileSize)
		if err != nil {
			return files, chunks, err
		}
		if err := insertMigratedGroup(ctx, pg, projectID, fileID, group, dims); err != nil {
			return files, chunks, err
		}
		files++
		chunks += len(group)
	}
	return files, chunks, nil
}

// nextFileGroup returns the run of rows starting at i that share the same file
// path and dims, plus the index just past that run.
func nextFileGroup(rows []localstore.ExportedChunk, i int) ([]localstore.ExportedChunk, int) {
	path, dims := rows[i].FilePath, rows[i].Dims
	j := i
	for j < len(rows) && rows[j].FilePath == path && rows[j].Dims == dims {
		j++
	}
	return rows[i:j], j
}

// ensureChunksTableOnce creates the chunks_<dims> table the first time a given
// dims is seen, tracking which have been ensured in the shared map.
func ensureChunksTableOnce(ctx context.Context, pg store.IndexStore, ensured map[int]bool, dims int) error {
	if ensured[dims] {
		return nil
	}
	if err := pg.EnsureChunksTable(ctx, dims); err != nil {
		return err
	}
	ensured[dims] = true
	return nil
}

// insertMigratedGroup inserts one file's chunks: text-only when any chunk has a
// NULL embedding (a sensitive file), otherwise with its copied embeddings.
func insertMigratedGroup(ctx context.Context, pg store.IndexStore, projectID, fileID int, group []localstore.ExportedChunk, dims int) error {
	cks, embs, textOnly := chunksFromGroup(group)
	if textOnly {
		return pg.InsertChunksTextOnly(ctx, projectID, fileID, cks, dims)
	}
	return pg.InsertChunks(ctx, projectID, fileID, cks, embs, dims)
}

// chunksFromGroup splits an exported group into chunks and embeddings, flagging
// the group text-only if any chunk was stored without an embedding.
func chunksFromGroup(group []localstore.ExportedChunk) (cks []chunker.Chunk, embs [][]float32, textOnly bool) {
	cks = make([]chunker.Chunk, len(group))
	embs = make([][]float32, len(group))
	for j, g := range group {
		cks[j] = chunker.Chunk{Content: g.Content, StartLine: g.StartLine, EndLine: g.EndLine}
		embs[j] = g.Embedding
		if g.Embedding == nil {
			textOnly = true
		}
	}
	return cks, embs, textOnly
}
