// Package localstore is a standalone, PostgreSQL-free implementation of
// store.IndexStore backed by a single pure-Go SQLite file (modernc.org/sqlite,
// no CGO). It lets the CLI index and search a project entirely locally — the
// on-disk index typically lives at ~/.local/share/semidx/index.db.
//
// Embeddings are stored as little-endian float32 BLOBs and similarity search is
// a brute-force cosine scan over a project's chunks. That is O(n) per query,
// which is fine at laptop/single-repo scale (tens of thousands of chunks); the
// server path (PgStore + pgvector HNSW) remains the choice for large corpora.
package localstore

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sqlite "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// SQLiteStore implements store.IndexStore over a local SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// compile-time assertion that SQLiteStore satisfies the indexing/search subset.
var _ store.IndexStore = (*SQLiteStore)(nil)

// projectColumns is the canonical projection order shared by the project
// getters so scanProject can read any of them.
const projectColumns = `id, name, path, model, status, source_type, git_url, branch`

// schema mirrors the pgvector layout conceptually as plain tables: an embedding
// BLOB replaces the vector column and there is one chunks table instead of the
// per-dimension chunks_<dims> tables (SQLite is dynamically typed, so a single
// table holds vectors of any dimension).
const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    path        TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'path',
    git_url     TEXT NOT NULL DEFAULT '',
    branch      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS files (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    hash       TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, path)
);
CREATE TABLE IF NOT EXISTS chunks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    file_id     INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    content     TEXT NOT NULL,
    embedding   BLOB,
    dims        INTEGER NOT NULL DEFAULT 0,
    model       TEXT NOT NULL DEFAULT '',
    start_line  INTEGER,
    end_line    INTEGER,
    UNIQUE(project_id, file_id, chunk_index)
);
CREATE INDEX IF NOT EXISTS idx_files_project ON files(project_id);
CREATE INDEX IF NOT EXISTS idx_chunks_project ON chunks(project_id);
CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_id);
`

// New opens (creating if absent) the SQLite database at path, creating the
// parent directory and schema as needed. foreign_keys is enabled per connection
// so ON DELETE CASCADE actually fires.
func New(path string) (*SQLiteStore, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	// _pragma params are applied by modernc on every pooled connection, so
	// foreign-key enforcement and a busy timeout hold across the whole pool.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() { _ = s.db.Close() }

// Ping verifies the database is reachable (used by /readyz parity).
func (s *SQLiteStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// EnsureChunksTable is a no-op: unlike pgvector there is a single, schemaless
// chunks table created on open, so there is no per-dimension table to build.
func (s *SQLiteStore) EnsureChunksTable(_ context.Context, _ int) error { return nil }

func (s *SQLiteStore) UpsertProject(ctx context.Context, name, path, model string) (int, error) {
	var id int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO projects (name, path, model, status, source_type)
		VALUES (?, ?, ?, 'indexing', 'path')
		ON CONFLICT(name) DO UPDATE SET path = excluded.path, model = excluded.model, status = 'indexing'
		RETURNING id
	`, name, path, model).Scan(&id)
	return id, err
}

// CreateProject registers a project with its content source, returning
// ErrProjectExists when the name is already taken.
func (s *SQLiteStore) CreateProject(ctx context.Context, name, model, sourceType, gitURL, branch string) (*store.Project, error) {
	var id int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO projects (name, path, model, status, source_type, git_url, branch)
		VALUES (?, '', ?, 'registered', ?, ?, ?)
		RETURNING id
	`, name, model, sourceType, gitURL, branch).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, store.ErrProjectExists
		}
		return nil, err
	}
	return &store.Project{ID: id, Name: name, Model: model, Status: "registered", SourceType: sourceType, GitURL: gitURL, Branch: branch}, nil
}

func scanProject(row interface{ Scan(...any) error }) (*store.Project, error) {
	var p store.Project
	if err := row.Scan(&p.ID, &p.Name, &p.Path, &p.Model, &p.Status, &p.SourceType, &p.GitURL, &p.Branch); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *SQLiteStore) GetProject(ctx context.Context, name string) (*store.Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return p, err
}

func (s *SQLiteStore) GetProjectByID(ctx context.Context, id int) (*store.Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return p, err
}

func (s *SQLiteStore) ListProjects(ctx context.Context) ([]store.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var projects []store.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, *p)
	}
	return projects, rows.Err()
}

// DeleteProject removes a project; files and chunks cascade. Returns
// ErrNotFound when no such project exists.
func (s *SQLiteStore) DeleteProject(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) UpdateProjectStatus(ctx context.Context, id int, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *SQLiteStore) UpsertFile(ctx context.Context, projectID int, path, hash string, size int) (int, error) {
	var id int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO files (project_id, path, hash, size_bytes)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(project_id, path) DO UPDATE
		SET hash = excluded.hash, size_bytes = excluded.size_bytes, indexed_at = datetime('now')
		RETURNING id
	`, projectID, path, hash, size).Scan(&id)
	return id, err
}

// FileUpToDate reports whether the file at path is already indexed with the
// given hash AND has at least one chunk, so the indexer can skip re-embedding it.
func (s *SQLiteStore) FileUpToDate(ctx context.Context, projectID int, path, hash string, _ int) (bool, error) {
	var fileID int
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM files WHERE project_id = ? AND path = ? AND hash = ?`,
		projectID, path, hash).Scan(&fileID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // absent or hash changed → needs indexing
	}
	if err != nil {
		return false, err
	}

	var exists bool
	err = s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM chunks WHERE file_id = ?)`, fileID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// ListFileHashes returns path→hash for every indexed file of a project.
func (s *SQLiteStore) ListFileHashes(ctx context.Context, projectID int) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, hash FROM files WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

// DeleteFileByPath removes a file and its chunks (cascade) by path.
func (s *SQLiteStore) DeleteFileByPath(ctx context.Context, projectID int, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE project_id = ? AND path = ?`, projectID, path)
	return err
}

func (s *SQLiteStore) DeleteChunksForFile(ctx context.Context, projectID, fileID, _ int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chunks WHERE project_id = ? AND file_id = ?`, projectID, fileID)
	return err
}

func (s *SQLiteStore) InsertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int) error {
	if len(chunks) != len(embeddings) {
		return fmt.Errorf("chunks and embeddings length mismatch: %d vs %d", len(chunks), len(embeddings))
	}
	model := s.projectModel(ctx, projectID)
	return s.insertChunks(ctx, projectID, fileID, chunks, embeddings, dims, model)
}

// InsertChunksTextOnly stores chunk content with a NULL embedding (sensitive
// files under a cloud-only model): the text stays keyword-searchable but is
// never embedded.
func (s *SQLiteStore) InsertChunksTextOnly(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, dims int) error {
	model := s.projectModel(ctx, projectID)
	return s.insertChunks(ctx, projectID, fileID, chunks, nil, dims, model)
}

// insertChunks upserts a batch of chunks in one transaction. When embeddings is
// nil the embedding column is stored NULL (text-only).
func (s *SQLiteStore) insertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int, model string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks (project_id, file_id, chunk_index, content, embedding, dims, model, start_line, end_line)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, file_id, chunk_index) DO UPDATE
		SET content = excluded.content, embedding = excluded.embedding, dims = excluded.dims,
		    model = excluded.model, start_line = excluded.start_line, end_line = excluded.end_line
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for i, chunk := range chunks {
		var blob any // NULL when text-only
		if embeddings != nil {
			blob = encodeEmbedding(embeddings[i])
		}
		if _, err := stmt.ExecContext(ctx, projectID, fileID, i, chunk.Content, blob, dims, model, chunk.StartLine, chunk.EndLine); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// projectModel returns a project's stored model, or "" if it can't be read.
func (s *SQLiteStore) projectModel(ctx context.Context, projectID int) string {
	var model string
	_ = s.db.QueryRowContext(ctx, `SELECT model FROM projects WHERE id = ?`, projectID).Scan(&model)
	return model
}

// SearchSimilar loads the project's embedded chunks and ranks them by cosine
// similarity to the query in Go. Brute force is acceptable at laptop scale; a
// large corpus should use the server's pgvector-backed ANN index instead.
func (s *SQLiteStore) SearchSimilar(ctx context.Context, projectID int, embedding []float32, _, topK int) ([]store.SearchResult, error) {
	// embedding IS NOT NULL excludes text-only rows (sensitive files stored
	// without an embedding); those surface via keyword search instead.
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.path, c.content, c.start_line, c.end_line, c.embedding
		FROM chunks c JOIN files f ON f.id = c.file_id
		WHERE c.project_id = ? AND c.embedding IS NOT NULL
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []store.SearchResult
	for rows.Next() {
		var (
			r         store.SearchResult
			startLine sql.NullInt64
			endLine   sql.NullInt64
			blob      []byte
		)
		if err := rows.Scan(&r.FilePath, &r.Content, &startLine, &endLine, &blob); err != nil {
			return nil, err
		}
		r.StartLine = int(startLine.Int64)
		r.EndLine = int(endLine.Int64)
		r.Score = cosineSimilarity(embedding, decodeEmbedding(blob))
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

// SearchSimilarKeywords finds chunks whose content matches every query word via
// SQL LIKE. Score is a constant 0.5, matching PgStore's keyword fallback.
func (s *SQLiteStore) SearchSimilarKeywords(ctx context.Context, projectID int, queryText string, _, topK int) ([]store.SearchResult, error) {
	words := strings.Fields(queryText)
	if len(words) == 0 {
		return nil, nil
	}

	clauses := make([]string, 0, len(words))
	args := []any{projectID}
	for _, w := range words {
		clauses = append(clauses, "c.content LIKE ?")
		args = append(args, "%"+w+"%")
	}
	if topK <= 0 {
		topK = -1 // SQLite treats LIMIT -1 as "no limit"
	}
	args = append(args, topK)

	// #nosec G201 -- the only interpolated fragment is a join of the constant
	// literal "c.content LIKE ?"; every user value is bound via ? parameters.
	query := fmt.Sprintf(`
		SELECT f.path, c.content, c.start_line, c.end_line, 0.5 AS score
		FROM chunks c JOIN files f ON f.id = c.file_id
		WHERE c.project_id = ? AND (%s)
		LIMIT ?
	`, strings.Join(clauses, " OR "))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []store.SearchResult
	for rows.Next() {
		var (
			r         store.SearchResult
			startLine sql.NullInt64
			endLine   sql.NullInt64
		)
		if err := rows.Scan(&r.FilePath, &r.Content, &startLine, &endLine, &r.Score); err != nil {
			return nil, err
		}
		r.StartLine = int(startLine.Int64)
		r.EndLine = int(endLine.Int64)
		results = append(results, r)
	}
	return results, rows.Err()
}

// DropAll clears all indexed data and resets the auto-increment counters
// (mirroring PgStore's TRUNCATE ... RESTART IDENTITY).
func (s *SQLiteStore) DropAll(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`DELETE FROM chunks`,
		`DELETE FROM files`,
		`DELETE FROM projects`,
		`DELETE FROM sqlite_sequence WHERE name IN ('chunks', 'files', 'projects')`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// encodeEmbedding serializes a float32 vector as little-endian bytes.
func encodeEmbedding(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// decodeEmbedding reverses encodeEmbedding.
func decodeEmbedding(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

// cosineSimilarity returns the cosine similarity of two equal-length vectors, or
// 0 when their lengths differ or either is a zero vector.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		normA += fa * fa
		normB += fb * fb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// isUniqueViolation reports whether err is a SQLite UNIQUE/PRIMARY KEY
// constraint failure, so CreateProject can map it to store.ErrProjectExists.
func isUniqueViolation(err error) bool {
	var serr *sqlite.Error
	if errors.As(err, &serr) {
		code := serr.Code()
		return code == sqlitelib.SQLITE_CONSTRAINT_UNIQUE || code == sqlitelib.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}
