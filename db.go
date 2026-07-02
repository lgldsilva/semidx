package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/lgldsilva/semidx/internal/chunker"
)

type DB struct {
	pool *pgxpool.Pool
}

type Project struct {
	ID     int
	Name   string
	Path   string
	Model  string
	Status string
}

type SearchResult struct {
	FilePath string
	Content  string
	Score    float64
}

func NewDB(ctx context.Context, connString string) (*DB, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, err
	}
	return &DB{pool: pool}, nil
}

func (db *DB) Close() {
	db.pool.Close()
}

func chunksTable(dims int) string {
	return fmt.Sprintf("chunks_%d", dims)
}

func (db *DB) EnsureChunksTable(ctx context.Context, dims int) error {
	tableName := chunksTable(dims)
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
		)`, tableName, dims)
	_, err := db.pool.Exec(ctx, query)
	if err != nil {
		return err
	}

	// Create indexes
	idxName := fmt.Sprintf("idx_chunks_%d_file", dims)
	_, err = db.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(file_id)", idxName, tableName))
	if err != nil {
		return err
	}
	idxNameProj := fmt.Sprintf("idx_chunks_%d_project", dims)
	_, err = db.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(project_id)", idxNameProj, tableName))
	return err
}

func (db *DB) UpsertProject(ctx context.Context, name, path, model string) (int, error) {
	var id int
	err := db.pool.QueryRow(ctx, `
		INSERT INTO projects (name, path, model, status)
		VALUES ($1, $2, $3, 'indexing')
		ON CONFLICT (name) DO UPDATE SET path = EXCLUDED.path, model = EXCLUDED.model, status = 'indexing'
		RETURNING id
	`, name, path, model).Scan(&id)
	return id, err
}

func (db *DB) GetProject(ctx context.Context, name string) (*Project, error) {
	var p Project
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, path, model, status FROM projects WHERE name = $1
	`, name).Scan(&p.ID, &p.Name, &p.Path, &p.Model, &p.Status)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (db *DB) UpdateProjectStatus(ctx context.Context, id int, status string) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE projects SET status = $1 WHERE id = $2
	`, status, id)
	return err
}

func (db *DB) UpsertFile(ctx context.Context, projectID int, path, hash string, size int) (int, error) {
	var id int
	err := db.pool.QueryRow(ctx, `
		INSERT INTO files (project_id, path, hash, size_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, path) DO UPDATE SET hash = EXCLUDED.hash, size_bytes = EXCLUDED.size_bytes, indexed_at = NOW()
		RETURNING id
	`, projectID, path, hash, size).Scan(&id)
	return id, err
}

func (db *DB) DeleteChunksForFile(ctx context.Context, projectID, fileID, dims int) error {
	tableName := chunksTable(dims)
	_, err := db.pool.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s WHERE project_id = $1 AND file_id = $2
	`, tableName), projectID, fileID)
	return err
}

func (db *DB) InsertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int) error {
	if len(chunks) != len(embeddings) {
		return fmt.Errorf("chunks and embeddings length mismatch: %d vs %d", len(chunks), len(embeddings))
	}

	tableName := chunksTable(dims)
	batch := &pgx.Batch{}
	for i, chunk := range chunks {
		vec := pgvector.NewVector(embeddings[i])
		batch.Queue(fmt.Sprintf(`
			INSERT INTO %s (project_id, file_id, chunk_index, content, embedding)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (project_id, file_id, chunk_index) DO UPDATE
			SET content = EXCLUDED.content, embedding = EXCLUDED.embedding
		`, tableName), projectID, fileID, i, chunk.Content, vec)
	}

	br := db.pool.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for range chunks {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return br.Close()
}

func (db *DB) SearchSimilar(ctx context.Context, projectID int, embedding []float32, dims, topK int) ([]SearchResult, error) {
	tableName := chunksTable(dims)
	query := fmt.Sprintf(`
		SELECT f.path, c.content, 1 - (c.embedding <=> $1) AS score
		FROM %s c
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = $2
		ORDER BY c.embedding <=> $1
		LIMIT $3
	`, tableName)

	vec := pgvector.NewVector(embedding)
	rows, err := db.pool.Query(ctx, query, vec, projectID, topK)
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

func (db *DB) DropAll(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `
		DO $$
		DECLARE
			tbl text;
		BEGIN
			FOR tbl IN SELECT tablename FROM pg_tables WHERE tablename LIKE 'chunks_%'
			LOOP
				EXECUTE 'DROP TABLE IF EXISTS ' || tbl || ' CASCADE';
			END LOOP;
		END $$;
		TRUNCATE files, projects RESTART IDENTITY CASCADE;
	`)
	return err
}

func (db *DB) SearchSimilarKeywords(ctx context.Context, projectID int, queryText string, dims, topK int) ([]SearchResult, error) {
	if dims <= 0 {
		// Varre as tabelas chunks_% existentes para achar qual tem dados deste projeto
		rows, err := db.pool.Query(ctx, "SELECT tablename FROM pg_tables WHERE tablename LIKE 'chunks_%'")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var tbl string
				if err := rows.Scan(&tbl); err == nil {
					var exists int
					checkQuery := fmt.Sprintf("SELECT 1 FROM %s WHERE project_id = $1 LIMIT 1", tbl)
					errCheck := db.pool.QueryRow(ctx, checkQuery, projectID).Scan(&exists)
					if errCheck == nil && exists == 1 {
						var d int
						_, errScan := fmt.Sscanf(tbl, "chunks_%d", &d)
						if errScan == nil && d > 0 {
							dims = d
							break
						}
					}
				}
			}
		}
	}
	if dims <= 0 {
		dims = 1024 // Fallback final se nenhuma tabela com dados for encontrada
	}

	tableName := chunksTable(dims)
	words := strings.Fields(queryText)
	if len(words) == 0 {
		return nil, nil
	}

	var clauses []string
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
	`, tableName, strings.Join(clauses, " OR "), len(args)+1)

	args = append(args, topK)

	rows, err := db.pool.Query(ctx, sql, args...)
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
