// Package store persists projects, files and per-dimension chunk vectors in
// PostgreSQL/pgvector. Callers depend on the Store interface, so the indexer and
// search layers can be unit-tested against a fake while PgStore is exercised by
// integration tests against a real pgvector container.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// maxDims bounds the embedding dimension used to name a chunks_<dims> table.
// It both rejects nonsense and hard-guards the dynamic table name (see ADR on
// per-dimension tables): dims is an int, so it can't inject, but validating it
// keeps the Sprintf'd identifier provably safe.
const maxDims = 16000

// hnswVectorLimit is pgvector's max dimension for an HNSW index over the
// `vector` type. Above it we index (and query) the `halfvec` cast instead.
const hnswVectorLimit = 2000

// Project is an indexed repository's metadata row.
type Project struct {
	ID         int
	Name       string
	Path       string
	Model      string
	Status     string
	SourceType string // "push" | "git" | "path"
	GitURL     string // set when SourceType == "git"
	Branch     string // optional git branch
}

// Sentinel errors so callers (e.g. the HTTP layer) can map to status codes
// without depending on pgx internals.
var (
	ErrNotFound      = errors.New("not found")
	ErrProjectExists = errors.New("project already exists")
)

// Token is an API token record (the plaintext is never stored, only its hash).
type Token struct {
	ID     int
	Name   string
	Scopes []string
}

// SearchResult is one hit from a similarity or keyword search.
type SearchResult struct {
	FilePath  string
	Content   string
	Score     float64
	StartLine int
	EndLine   int
}

// Store is the persistence surface the rest of semidx depends on.
type Store interface {
	Close()
	Ping(ctx context.Context) error
	EnsureChunksTable(ctx context.Context, dims int) error
	UpsertProject(ctx context.Context, name, path, model string) (int, error)
	CreateProject(ctx context.Context, name, model, sourceType, gitURL, branch string) (*Project, error)
	GetProject(ctx context.Context, name string) (*Project, error)
	ListProjects(ctx context.Context) ([]Project, error)
	DeleteProject(ctx context.Context, name string) error
	UpdateProjectStatus(ctx context.Context, id int, status string) error
	UpsertFile(ctx context.Context, projectID int, path, hash string, size int) (int, error)
	FileUpToDate(ctx context.Context, projectID int, path, hash string, dims int) (bool, error)
	ListFileHashes(ctx context.Context, projectID int) (map[string]string, error)
	DeleteFileByPath(ctx context.Context, projectID int, path string) error
	DeleteChunksForFile(ctx context.Context, projectID, fileID, dims int) error
	InsertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int) error
	InsertChunksTextOnly(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, dims int) error
	SearchSimilar(ctx context.Context, projectID int, embedding []float32, dims, topK int) ([]SearchResult, error)
	SearchSimilarKeywords(ctx context.Context, projectID int, queryText string, dims, topK int) ([]SearchResult, error)
	DropAll(ctx context.Context) error

	CreateToken(ctx context.Context, name, tokenHash string, scopes []string) (int, error)
	TokenByHash(ctx context.Context, tokenHash string) (*Token, error)
	RevokeToken(ctx context.Context, id int) error
	CountTokens(ctx context.Context) (int, error)

	EnqueueJob(ctx context.Context, projectID int, jobType string) (int, error)
	ClaimJob(ctx context.Context) (*Job, error)
	CompleteJob(ctx context.Context, id, filesIndexed, chunksCreated int) error
	FailJob(ctx context.Context, id int, errMsg string) error
	GetJob(ctx context.Context, id int) (*Job, error)
	GetProjectByID(ctx context.Context, id int) (*Project, error)
}

// PgStore is the PostgreSQL/pgvector implementation of Store.
type PgStore struct {
	pool *pgxpool.Pool
}

// compile-time assertion that PgStore satisfies Store.
var _ Store = (*PgStore)(nil)

// NewPgStore connects (and pings) a pgxpool at connString and applies any
// pending schema migrations.
func NewPgStore(ctx context.Context, connString string) (*PgStore, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := migrate(ctx, connString); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return &PgStore{pool: pool}, nil
}

func (s *PgStore) Close() {
	s.pool.Close()
}

// Ping verifies the database connection (used by /readyz).
func (s *PgStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
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
			start_line INTEGER,
			end_line INTEGER,
			created_at TIMESTAMP DEFAULT NOW(),
			UNIQUE(project_id, file_id, chunk_index),
			FOREIGN KEY (file_id) REFERENCES files(id) ON DELETE CASCADE,
			FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
		)`, table, dims)
	if _, err := s.pool.Exec(ctx, query); err != nil {
		return err
	}

	// Upgrade tables created before line tracking existed.
	for _, col := range []string{"start_line", "end_line"} {
		if _, err := s.pool.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s INTEGER", table, col)); err != nil {
			return err
		}
	}

	idxFile := pgx.Identifier{fmt.Sprintf("idx_chunks_%d_file", dims)}.Sanitize()
	if _, err := s.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(file_id)", idxFile, table)); err != nil {
		return err
	}
	idxProj := pgx.Identifier{fmt.Sprintf("idx_chunks_%d_project", dims)}.Sanitize()
	if _, err := s.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s(project_id)", idxProj, table)); err != nil {
		return err
	}

	// ANN index for cosine similarity. HNSW over `vector` maxes out at 2000
	// dims, so higher-dimensional models (e.g. Gemini 3072) index the halfvec
	// cast — SearchSimilar queries the matching expression so the index is used.
	idxHNSW := pgx.Identifier{fmt.Sprintf("idx_chunks_%d_hnsw", dims)}.Sanitize()
	opclass := "(embedding vector_cosine_ops)"
	if dims > hnswVectorLimit {
		opclass = fmt.Sprintf("((embedding::halfvec(%d)) halfvec_cosine_ops)", dims)
	}
	_, err = s.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw %s", idxHNSW, table, opclass))
	return err
}

// distanceExpr is the cosine-distance SQL expression against query param $1,
// matching the HNSW index built for the given dimension.
func distanceExpr(dims int) string {
	if dims > hnswVectorLimit {
		return fmt.Sprintf("c.embedding::halfvec(%d) <=> $1::halfvec(%d)", dims, dims)
	}
	return "c.embedding <=> $1"
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

const projectColumns = `id, name, path, model, status, source_type, COALESCE(git_url, ''), COALESCE(branch, '')`

func scanProject(row pgx.Row) (*Project, error) {
	var p Project
	if err := row.Scan(&p.ID, &p.Name, &p.Path, &p.Model, &p.Status, &p.SourceType, &p.GitURL, &p.Branch); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *PgStore) GetProject(ctx context.Context, name string) (*Project, error) {
	p, err := scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE name = $1`, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// CreateProject registers a project with its content source. Returns
// ErrProjectExists if the name is taken.
func (s *PgStore) CreateProject(ctx context.Context, name, model, sourceType, gitURL, branch string) (*Project, error) {
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (name, path, model, status, source_type, git_url, branch)
		VALUES ($1, '', $2, 'registered', $3, NULLIF($4, ''), NULLIF($5, ''))
		RETURNING id
	`, name, model, sourceType, gitURL, branch).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return nil, ErrProjectExists
		}
		return nil, err
	}
	return &Project{ID: id, Name: name, Model: model, Status: "registered", SourceType: sourceType, GitURL: gitURL, Branch: branch}, nil
}

func (s *PgStore) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, *p)
	}
	return projects, rows.Err()
}

// DeleteProject removes a project (files and chunks cascade). Returns
// ErrNotFound when no such project exists.
func (s *PgStore) DeleteProject(ctx context.Context, name string) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE name = $1`, name)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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

// FileUpToDate reports whether the file at path is already indexed with the
// given content hash AND has at least one chunk in the chunks_<dims> table.
// When true, the indexer can skip re-embedding it (incremental indexing).
func (s *PgStore) FileUpToDate(ctx context.Context, projectID int, path, hash string, dims int) (bool, error) {
	table, err := chunksTable(dims)
	if err != nil {
		return false, err
	}

	var fileID int
	err = s.pool.QueryRow(ctx,
		`SELECT id FROM files WHERE project_id = $1 AND path = $2 AND hash = $3`,
		projectID, path, hash).Scan(&fileID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // absent or hash changed → needs indexing
	}
	if err != nil {
		return false, err
	}

	var exists bool
	err = s.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT EXISTS(SELECT 1 FROM %s WHERE file_id = $1)`, table), fileID).Scan(&exists)
	if err != nil {
		return false, nil // e.g. table not created yet → treat as needs indexing
	}
	return exists, nil
}

// ListFileHashes returns path→hash for every indexed file of a project (used by
// the push files/diff endpoint to decide what changed).
func (s *PgStore) ListFileHashes(ctx context.Context, projectID int) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT path, hash FROM files WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		out[path] = hash
	}
	return out, rows.Err()
}

// DeleteFileByPath removes a file and its chunks (FK cascade) by path.
func (s *PgStore) DeleteFileByPath(ctx context.Context, projectID int, path string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM files WHERE project_id = $1 AND path = $2`, projectID, path)
	return err
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
			INSERT INTO %s (project_id, file_id, chunk_index, content, embedding, start_line, end_line)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (project_id, file_id, chunk_index) DO UPDATE
			SET content = EXCLUDED.content, embedding = EXCLUDED.embedding,
			    start_line = EXCLUDED.start_line, end_line = EXCLUDED.end_line
		`, table), projectID, fileID, i, chunk.Content, vec, chunk.StartLine, chunk.EndLine)
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
			INSERT INTO %s (project_id, file_id, chunk_index, content, embedding, start_line, end_line)
			VALUES ($1, $2, $3, $4, NULL, $5, $6)
			ON CONFLICT (project_id, file_id, chunk_index) DO UPDATE
			SET content = EXCLUDED.content, embedding = NULL,
			    start_line = EXCLUDED.start_line, end_line = EXCLUDED.end_line
		`, table), projectID, fileID, i, chunk.Content, chunk.StartLine, chunk.EndLine)
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
	dist := distanceExpr(dims)
	query := fmt.Sprintf(`
		SELECT f.path, c.content, c.start_line, c.end_line, 1 - (%s) AS score
		FROM %s c
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = $2 AND c.embedding IS NOT NULL
		ORDER BY %s
		LIMIT $3
	`, dist, table, dist)

	vec := pgvector.NewVector(embedding)
	rows, err := s.pool.Query(ctx, query, vec, projectID, topK)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSearchRows(rows)
}

// scanSearchRows reads (path, content, start_line, end_line, score) rows,
// tolerating NULL line columns (rows indexed before line tracking).
func scanSearchRows(rows pgx.Rows) ([]SearchResult, error) {
	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var startLine, endLine *int
		if err := rows.Scan(&r.FilePath, &r.Content, &startLine, &endLine, &r.Score); err != nil {
			return nil, err
		}
		if startLine != nil {
			r.StartLine = *startLine
		}
		if endLine != nil {
			r.EndLine = *endLine
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// CreateToken stores a new API token (by hash) and returns its id.
func (s *PgStore) CreateToken(ctx context.Context, name, tokenHash string, scopes []string) (int, error) {
	if scopes == nil {
		scopes = []string{}
	}
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (name, token_hash, scopes) VALUES ($1, $2, $3) RETURNING id
	`, name, tokenHash, scopes).Scan(&id)
	return id, err
}

// TokenByHash returns the non-revoked token with the given hash and records its
// use. Returns (nil, nil) when no active token matches.
func (s *PgStore) TokenByHash(ctx context.Context, tokenHash string) (*Token, error) {
	var t Token
	err := s.pool.QueryRow(ctx, `
		UPDATE api_tokens SET last_used_at = NOW()
		WHERE token_hash = $1 AND revoked_at IS NULL
		RETURNING id, name, scopes
	`, tokenHash).Scan(&t.ID, &t.Name, &t.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// RevokeToken marks a token revoked (idempotent).
func (s *PgStore) RevokeToken(ctx context.Context, id int) error {
	_, err := s.pool.Exec(ctx, `UPDATE api_tokens SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

// CountTokens returns how many non-revoked tokens exist (used for bootstrap).
func (s *PgStore) CountTokens(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL`).Scan(&n)
	return n, err
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
		SELECT f.path, c.content, c.start_line, c.end_line, 0.5 AS score
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

	return scanSearchRows(rows)
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
