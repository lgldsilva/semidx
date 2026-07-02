// Package store persists projects, files and per-dimension chunk vectors in
// PostgreSQL/pgvector. Callers depend on the Store interface, so the indexer and
// search layers can be unit-tested against a fake while PgStore is exercised by
// integration tests against a real pgvector container.
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// maxDims bounds the embedding dimension used to name a chunks_<dims> table.
// It both rejects nonsense and hard-guards the dynamic table name (see ADR on
// per-dimension tables): dims is an int, so it can't inject, but validating it
// keeps the Sprintf'd identifier provably safe.
const maxDims = 16000

// Project is an indexed repository's metadata row.
type Project struct {
	ID     int
	Name   string
	Path   string
	Model  string
	Status string
}

// SearchResult is one hit from a similarity or keyword search.
type SearchResult struct {
	FilePath string
	Content  string
	Score    float64
}

// Store is the persistence surface the rest of semidx depends on.
type Store interface {
	Close()
	EnsureChunksTable(ctx context.Context, dims int) error
	UpsertProject(ctx context.Context, name, path, model string) (int, error)
	GetProject(ctx context.Context, name string) (*Project, error)
	UpdateProjectStatus(ctx context.Context, id int, status string) error
	UpsertFile(ctx context.Context, projectID int, path, hash string, size int) (int, error)
	DeleteChunksForFile(ctx context.Context, projectID, fileID, dims int) error
	InsertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int) error
	InsertChunksTextOnly(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, dims int) error
	SearchSimilar(ctx context.Context, projectID int, embedding []float32, dims, topK int) ([]SearchResult, error)
	SearchSimilarKeywords(ctx context.Context, projectID int, queryText string, dims, topK int) ([]SearchResult, error)
	DropAll(ctx context.Context) error
}

// PgStore is the PostgreSQL/pgvector implementation of Store.
type PgStore struct {
	pool *pgxpool.Pool
}

// compile-time assertion that PgStore satisfies Store.
var _ Store = (*PgStore)(nil)

// NewPgStore connects (and pings) a pgxpool at connString.
func NewPgStore(ctx context.Context, connString string) (*PgStore, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}
	return &PgStore{pool: pool}, nil
}

func (s *PgStore) Close() {
	s.pool.Close()
}

// chunksTable returns the safely-quoted table identifier for a dimension, or an
// error if the dimension is out of range.
func chunksTable(dims int) (string, error) {
	if dims < 1 || dims > maxDims {
		return "", fmt.Errorf("invalid embedding dimension %d (must be 1..%d)", dims, maxDims)
	}
	return pgx.Identifier{fmt.Sprintf("chunks_%d", dims)}.Sanitize(), nil
}

func (s *PgStore) EnsureChunksTable(ctx context.Context, dims int) error {
	table, err := chunksTable(dims)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id SERIAL PRIMARY KEY,
			project_id INTEGER,
			file_id INTEGER,
			chunk_index INTEGER NOT NULL,
			content TEXT NOT NULL,
			embedding vector(%d),
			created_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(project_id, file_id, chunk_index),
			FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE,
			FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
		)`, table, dims)
	if _, err := s.pool.Exec(ctx, query); err != nil {
		return err
	}

	idxFile := pgx.Identifier{fmt.Sprintf("idx_chunks_%d_file", dims)}.Sanitize()
	if _, err := s.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(file_id)", idxFile, table)); err != nil {
		return err
	}
	idxProj := pgx.Identifier{fmt.Sprintf("idx_chunks_%d_project", dims)}.Sanitize()
	_, err = s.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(project_id)", idxProj, table))
	return err
}

func (s *PgStore) UpsertProject(ctx context.Context, name, path, model string) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (name, path, model, status)
		VALUES ($1, $2, $3, 'indexing')
		ON CONFLICT (name) DO UPDATE SET path = EXCLUDED.path, model = EXCLUDED.model, status = 'indexing'
		RETURNING id
	`, name, path, model).Scan(&id)
	return id, err
}

func (s *PgStore) GetProject(ctx context.Context, name string) (*Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, path, model, status FROM projects WHERE name = $1
	`, name).Scan(&p.ID, &p.Name, &p.Path, &p.Model, &p.Status)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *PgStore) UpdateProjectStatus(ctx context.Context, id int, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET status = $1 WHERE id = $2`, status, id)
	return err
}

func (s *PgStore) UpsertFile(ctx context.Context, projectID int, path, hash string, size int) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO files (project_id, path, hash, size_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, path) DO UPDATE SET hash = EXCLUDED.hash, size_bytes = EXCLUDED.size_bytes, indexed_at = NOW()
		RETURNING id
	`, projectID, path, hash, size).Scan(&id)
	return id, err
}

func (s *PgStore) DeleteChunksForFile(ctx context.Context, projectID, fileID, dims int) error {
	table, err := chunksTable(dims)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE project_id = $1 AND file_id = $2`, table), projectID, fileID)
	return err
}

func (s *PgStore) InsertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int) error {
	if len(chunks) != len(embeddings) {
		return fmt.Errorf("chunks and embeddings length mismatch: %d vs %d", len(chunks), len(embeddings))
	}
	table, err := chunksTable(dims)
	if err != nil {
		return err
	}

	batch := &pgx.Batch{}
	for i, chunk := range chunks {
		vec := pgvector.NewVector(embeddings[i])
		batch.Queue(fmt.Sprintf(`
			INSERT INTO %s (project_id, file_id, chunk_index, content, embedding)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (project_id, file_id, chunk_index) DO UPDATE
			SET content = EXCLUDED.content, embedding = EXCLUDED.embedding
		`, table), projectID, fileID, i, chunk.Content, vec)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for range chunks {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

// InsertChunksTextOnly stores chunk content with a NULL embedding. Used for
// sensitive files under a cloud-only model: the text stays searchable via the
// keyword (ILIKE) fallback but is never sent to a cloud embedding provider.
func (s *PgStore) InsertChunksTextOnly(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, dims int) error {
	table, err := chunksTable(dims)
	if err != nil {
		return err
	}

	batch := &pgx.Batch{}
	for i, chunk := range chunks {
		batch.Queue(fmt.Sprintf(`
			INSERT INTO %s (project_id, file_id, chunk_index, content, embedding)
			VALUES ($1, $2, $3, $4, NULL)
			ON CONFLICT (project_id, file_id, chunk_index) DO UPDATE
			SET content = EXCLUDED.content, embedding = NULL
		`, table), projectID, fileID, i, chunk.Content)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for range chunks {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

func (s *PgStore) SearchSimilar(ctx context.Context, projectID int, embedding []float32, dims, topK int) ([]SearchResult, error) {
	table, err := chunksTable(dims)
	if err != nil {
		return nil, err
	}
	// embedding IS NOT NULL excludes text-only rows (sensitive files stored
	// without an embedding); those surface via keyword search instead.
	query := fmt.Sprintf(`
		SELECT f.path, c.content, 1 - (c.embedding <=> $1) AS score
		FROM %s c
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = $2 AND c.embedding IS NOT NULL
		ORDER BY c.embedding <=> $1
		LIMIT $3
	`, table)

	vec := pgvector.NewVector(embedding)
	rows, err := s.pool.Query(ctx, query, vec, projectID, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.FilePath, &r.Content, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *PgStore) DropAll(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		DO $$
		DECLARE
			tbl text;
		BEGIN
			FOR tbl IN SELECT tablename FROM pg_tables WHERE tablename LIKE 'chunks_%'
			LOOP
				EXECUTE 'DROP TABLE IF EXISTS ' || quote_ident(tbl) || ' CASCADE';
			END LOOP;
		END $$;
		TRUNCATE files, projects RESTART IDENTITY CASCADE;
	`)
	return err
}

func (s *PgStore) SearchSimilarKeywords(ctx context.Context, projectID int, queryText string, dims, topK int) ([]SearchResult, error) {
	if dims <= 0 {
		dims = s.probeDimsForProject(ctx, projectID)
	}
	if dims <= 0 {
		dims = 1024 // final fallback when no chunks_* table has rows for the project
	}

	table, err := chunksTable(dims)
	if err != nil {
		return nil, err
	}

	words := strings.Fields(queryText)
	if len(words) == 0 {
		return nil, nil
	}

	clauses := make([]string, 0, len(words))
	args := []any{projectID}
	for i, w := range words {
		clauses = append(clauses, fmt.Sprintf("c.content ILIKE $%d", i+2))
		args = append(args, "%"+w+"%")
	}

	sql := fmt.Sprintf(`
		SELECT f.path, c.content, 0.5 AS score
		FROM %s c
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = $1 AND (%s)
		LIMIT $%d
	`, table, strings.Join(clauses, " OR "), len(args)+1)
	args = append(args, topK)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.FilePath, &r.Content, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// probeDimsForProject scans existing chunks_<dims> tables for one holding rows
// of the given project, returning its dimension (0 if none found).
func (s *PgStore) probeDimsForProject(ctx context.Context, projectID int) int {
	rows, err := s.pool.Query(ctx, "SELECT tablename FROM pg_tables WHERE tablename LIKE 'chunks_%'")
	if err != nil {
		return 0
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var tbl string
		if err := rows.Scan(&tbl); err == nil {
			tables = append(tables, tbl)
		}
	}

	for _, tbl := range tables {
		var exists int
		q := fmt.Sprintf("SELECT 1 FROM %s WHERE project_id = $1 LIMIT 1", pgx.Identifier{tbl}.Sanitize())
		if err := s.pool.QueryRow(ctx, q, projectID).Scan(&exists); err == nil && exists == 1 {
			var d int
			if _, err := fmt.Sscanf(tbl, "chunks_%d", &d); err == nil && d > 0 {
				return d
			}
		}
	}
	return 0
}
