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
	"github.com/lgldsilva/semidx/internal/keyword"
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
const projectColumns = `id, name, path, model, status, source_type, git_url, branch, COALESCE(identity, ''), COALESCE(dims, 0)`

// schema mirrors the pgvector layout conceptually as plain tables: an embedding
// BLOB replaces the vector column and there is one chunks table instead of the
// per-dimension chunks_<dims> tables (SQLite is dynamically typed, so a single
// table holds vectors of any dimension).
const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL DEFAULT '',
    model       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'path',
    git_url     TEXT NOT NULL DEFAULT '',
    branch      TEXT NOT NULL DEFAULT '',
    identity    TEXT,
    dims        INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_identity ON projects(identity);
CREATE TABLE IF NOT EXISTS files (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    hash       TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(project_id, path, hash)
);
CREATE TABLE IF NOT EXISTS worktree_files (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    worktree   TEXT NOT NULL,
    path       TEXT NOT NULL,
    hash       TEXT NOT NULL,
    PRIMARY KEY (project_id, worktree, path)
);
CREATE INDEX IF NOT EXISTS idx_worktree_files ON worktree_files(project_id, worktree);
CREATE TABLE IF NOT EXISTS file_dependencies (
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_file TEXT    NOT NULL,
    target_file TEXT    NOT NULL,
    PRIMARY KEY (project_id, source_file, target_file)
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

CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    content,
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS chunks_fts_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, content) VALUES (new.id, new.content);
END;

CREATE TRIGGER IF NOT EXISTS chunks_fts_bd BEFORE DELETE ON chunks BEGIN
    DELETE FROM chunks_fts WHERE rowid = old.id;
END;

CREATE TRIGGER IF NOT EXISTS chunks_fts_au AFTER UPDATE ON chunks BEGIN
    DELETE FROM chunks_fts WHERE rowid = old.id;
    INSERT INTO chunks_fts(rowid, content) VALUES (new.id, new.content);
END;
`

// schemaLockPath returns the path to a lock file used to serialise schema
// initialisation across processes (and goroutines). The lock file sits beside
// the database and is never removed — it is empty and harmless.
func schemaLockPath(dbPath string) string { return dbPath + ".lock" }

// New opens (creating if absent) the SQLite database at path, creating the
// parent directory and schema as needed. foreign_keys is enabled per connection
// so ON DELETE CASCADE actually fires.
func New(path string) (*SQLiteStore, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	// Cross-process lock to serialise schema initialisation: SQLite's
	// busy_timeout handles concurrent reads/writes during normal operation,
	// but FTS5 virtual-table creation and trigger setup can race when two
	// processes (e.g. index + search) call ensureSchema simultaneously.
	lockPath := schemaLockPath(path)
	// #nosec G304 -- lockPath is a fixed .lock sidecar next to the db file,
	// intentionally derived from the user-provided db path (same trust model as the
	// db file itself opened via sql.Open below).
	lockFile, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open schema lock: %w", err)
	}
	if err := flockExclusive(lockFile); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("lock schema: %w", err)
	}
	// Keep the lock held until ensureSchema completes, then release so other
	// waiters can also verify the schema (IF NOT EXISTS handles idempotency).
	defer func() {
		_ = flockUnlock(lockFile)
		_ = lockFile.Close()
	}()

	// Anti-corruption for concurrent access (multiple semidx processes / the
	// indexer's worker pool share one file): WAL is crash-resilient and allows
	// concurrent readers with one writer; synchronous=NORMAL is durable enough for
	// a re-derivable index (a crash at worst loses the last transaction); a long
	// busy_timeout makes writers wait instead of racing. _pragma params apply on
	// every pooled connection.
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Serialize writes through a single connection: SQLite allows one writer at a
	// time, so a single conn removes lock contention entirely (fine at laptop
	// scale) and guarantees no corruption from concurrent goroutines/processes.
	db.SetMaxOpenConns(1)
	if err := ensureSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// ensureSchema creates the tables, and — because the local index is a
// re-derivable cache with no migration tooling — transparently rebuilds an older
// pre-F11 database (a projects table without the content-addressing 'identity'
// column) by dropping and recreating it. Re-indexing repopulates it.
func ensureSchema(db *sql.DB) error {
	var cols int
	_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('projects')`).Scan(&cols)
	if cols > 0 {
		var hasIdentity int
		_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name='identity'`).Scan(&hasIdentity)
		var ddl string
		_ = db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='projects'`).Scan(&ddl)
		// Rebuild an older DB: missing the 'identity' column (pre-F11), OR still
		// enforcing UNIQUE on the projects table — i.e. UNIQUE(name) (pre-F14),
		// which wrongly blocks two projects that share a basename.
		if hasIdentity == 0 || strings.Contains(strings.ToUpper(ddl), "UNIQUE") {
			for _, tbl := range []string{"chunks_fts", "chunks", "worktree_files", "files", "projects"} {
				if _, err := db.Exec("DROP TABLE IF EXISTS " + tbl); err != nil {
					return err
				}
			}
		}
	}
	_, err := db.Exec(schema)
	return err
}

func (s *SQLiteStore) Close() { _ = s.db.Close() }

// Ping verifies the database is reachable (used by /readyz parity).
func (s *SQLiteStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// EnsureChunksTable is a no-op: unlike pgvector there is a single, schemaless
// chunks table created on open, so there is no per-dimension table to build.
func (s *SQLiteStore) EnsureChunksTable(_ context.Context, _ int) error { return nil }

func (s *SQLiteStore) UpsertProject(ctx context.Context, name, path, model string, dims int) (int, error) {
	var id int
	// name is no longer UNIQUE (F14), so upsert on identity instead — for this
	// legacy by-name API the identity is the name, keeping it idempotent per name.
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO projects (name, path, model, status, source_type, identity, dims)
		VALUES (?, ?, ?, 'indexing', 'path', ?, ?)
		ON CONFLICT(identity) DO UPDATE SET path = excluded.path, model = excluded.model, status = 'indexing', dims = excluded.dims
		RETURNING id
	`, name, path, model, name, dims).Scan(&id)
	return id, err
}

// EnsureProjectIdentity upserts a project keyed by its stable identity so all
// worktrees of a repo map to one row.
func (s *SQLiteStore) EnsureProjectIdentity(ctx context.Context, identity, name, path, model, sourceType string, dims int) (int, error) {
	var id int
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO projects (name, path, model, status, source_type, identity, dims)
		VALUES (?, ?, ?, 'indexing', ?, ?, ?)
		ON CONFLICT(identity) DO UPDATE SET path = excluded.path, model = excluded.model, status = 'indexing', dims = excluded.dims
		RETURNING id
	`, name, path, model, sourceType, identity, dims).Scan(&id)
	return id, err
}

// SetWorktreeFiles replaces a worktree's manifest (its path->hash set) atomically.
func (s *SQLiteStore) SetWorktreeFiles(ctx context.Context, projectID int, worktree string, files map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	if _, err := tx.ExecContext(ctx, `DELETE FROM worktree_files WHERE project_id = ? AND worktree = ?`, projectID, worktree); err != nil {
		return err
	}
	if len(files) == 0 {
		return tx.Commit()
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO worktree_files (project_id, worktree, path, hash) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for path, hash := range files {
		if _, err := stmt.ExecContext(ctx, projectID, worktree, path, hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneUnreferencedFiles deletes files (and, via ON DELETE CASCADE, chunks) that
// no worktree of the project still references, bounding index growth. Returns the
// number of files removed.
func (s *SQLiteStore) PruneUnreferencedFiles(ctx context.Context, projectID int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM files
		WHERE project_id = ?
		  AND NOT EXISTS (
		    SELECT 1 FROM worktree_files w
		    WHERE w.project_id = files.project_id AND w.path = files.path AND w.hash = files.hash
		  )
	`, projectID)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM file_dependencies
		WHERE project_id = ?
		  AND NOT EXISTS (
		    SELECT 1 FROM files f
		    WHERE f.project_id = file_dependencies.project_id AND f.path = file_dependencies.source_file
		  )
	`, projectID); err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CreateProject registers a project with its content source, returning
// ErrProjectExists when the name is already taken.
func (s *SQLiteStore) CreateProject(ctx context.Context, name, model, sourceType, gitURL, branch string, dims int) (*store.Project, error) {
	var id int
	// identity = name (see PgStore.CreateProject): name uniqueness for registered
	// projects is enforced via the identity unique index, since UNIQUE(name) is gone.
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO projects (name, path, model, status, source_type, git_url, branch, identity, dims)
		VALUES (?, '', ?, 'registered', ?, ?, ?, ?, ?)
		RETURNING id
	`, name, model, sourceType, gitURL, branch, name, dims).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, store.ErrProjectExists
		}
		return nil, err
	}
	return &store.Project{ID: id, Name: name, Model: model, Status: "registered", SourceType: sourceType, GitURL: gitURL, Branch: branch, Identity: name, Dims: dims}, nil
}

func scanProject(row interface{ Scan(...any) error }) (*store.Project, error) {
	var p store.Project
	if err := row.Scan(&p.ID, &p.Name, &p.Path, &p.Model, &p.Status, &p.SourceType, &p.GitURL, &p.Branch, &p.Identity, &p.Dims); err != nil {
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

// GetProjectByIdentity looks a project up by its unique identity (git identity
// or "path:<abs>") rather than the collision-prone basename.
func (s *SQLiteStore) GetProjectByIdentity(ctx context.Context, identity string) (*store.Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE identity = ?`, identity))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return p, err
}

func (s *SQLiteStore) ListProjects(ctx context.Context, limit, offset int) ([]store.Project, error) {
	if limit <= 0 {
		limit = -1 // SQLite treats LIMIT -1 as "no limit"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY name LIMIT ? OFFSET ?`, limit, offset)
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
	// Content-addressed: (project, path, hash) is unique so divergent versions of
	// the same path coexist across worktrees.
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO files (project_id, path, hash, size_bytes)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(project_id, path, hash) DO UPDATE
		SET size_bytes = excluded.size_bytes, indexed_at = datetime('now')
		RETURNING id
	`, projectID, path, hash, size).Scan(&id)
	return id, err
}

// FileUpToDate reports whether the file at path is already indexed with the
// given hash AND has at least one chunk, so the indexer can skip re-embedding it.
func (s *SQLiteStore) FileUpToDate(ctx context.Context, projectID int, path, hash string, dims int) (bool, error) {
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

	// Scope the check to the requested embedding dimension: the same file may
	// already have chunks under a DIFFERENT model/dims (e.g. keyword-only, or a
	// prior model). Ignoring dims would wrongly skip re-embedding for the new
	// bucket, leaving semantic search for that model empty.
	var exists bool
	err = s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM chunks WHERE file_id = ? AND dims = ?)`, fileID, dims).Scan(&exists)
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
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM file_dependencies
		WHERE project_id = ? AND (source_file = ? OR target_file = ?)
	`, projectID, path, path); err != nil {
		return err
	}
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
	return s.searchSimilar(ctx, projectID, embedding, topK, "")
}

// SearchSimilarWorktree restricts the scan to a worktree's checked-out versions.
func (s *SQLiteStore) SearchSimilarWorktree(ctx context.Context, projectID int, embedding []float32, _, topK int, worktree string) ([]store.SearchResult, error) {
	return s.searchSimilar(ctx, projectID, embedding, topK, worktree)
}

func (s *SQLiteStore) searchSimilar(ctx context.Context, projectID int, embedding []float32, topK int, worktree string) ([]store.SearchResult, error) {
	// embedding IS NOT NULL excludes text-only rows (sensitive files stored
	// without an embedding); those surface via keyword search instead.
	join := "JOIN files f ON f.id = c.file_id"
	args := []any{projectID}
	if worktree != "" {
		join = "JOIN files f ON f.id = c.file_id JOIN worktree_files w ON w.project_id = c.project_id AND w.path = f.path AND w.hash = f.hash AND w.worktree = ?"
		args = []any{worktree, projectID}
	}
	// #nosec G201 -- join is one of two constant literals; all values are bound as ? params.
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.path, c.content, c.start_line, c.end_line, c.embedding
		FROM chunks c `+join+`
		WHERE c.project_id = ? AND c.embedding IS NOT NULL
	`, args...)
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

	if topK <= 0 || topK >= len(results) {
		sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
		return results, nil
	}
	// Min-heap of size topK: keep only the top-K scoring results during the
	// scan, reducing O(n log n) sort + O(n) memory to O(n) scan + O(k log k)
	// sort + O(k) memory. For typical topK=20 with 50k chunks this is a
	// significant improvement.
	top := make([]store.SearchResult, topK)
	copy(top, results[:topK])
	for j := topK/2 - 1; j >= 0; j-- {
		siftDown(top, j, topK)
	}
	for i := topK; i < len(results); i++ {
		if results[i].Score > top[0].Score {
			top[0] = results[i]
			siftDown(top, 0, topK)
		}
	}
	sort.SliceStable(top, func(i, j int) bool { return top[i].Score > top[j].Score })
	return top, nil
}

// SearchSimilarKeywords finds chunks whose content matches every query word via
// SQL LIKE. Score is a constant 0.5, matching PgStore's keyword fallback.
func (s *SQLiteStore) SearchSimilarKeywords(ctx context.Context, projectID int, queryText string, _, topK int) ([]store.SearchResult, error) {
	return s.searchKeywords(ctx, projectID, queryText, topK, "")
}

// SearchSimilarKeywordsWorktree restricts the keyword search to a worktree's versions.
func (s *SQLiteStore) SearchSimilarKeywordsWorktree(ctx context.Context, projectID int, queryText string, _, topK int, worktree string) ([]store.SearchResult, error) {
	return s.searchKeywords(ctx, projectID, queryText, topK, worktree)
}

func (s *SQLiteStore) searchKeywords(ctx context.Context, projectID int, queryText string, topK int, worktree string) ([]store.SearchResult, error) {
	words := keyword.FilterSearchWords(queryText)
	if len(words) == 0 {
		return nil, nil
	}

	// Build a FTS5 MATCH query with OR semantics (any word matches).
	// Quote each word to handle special FTS5 characters.
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	ftsQuery := strings.Join(quoted, " OR ")

	// Build the worktree join if filtering by worktree.
	join := "JOIN files f ON f.id = c.file_id"
	var args []any
	if worktree != "" {
		join = "JOIN files f ON f.id = c.file_id JOIN worktree_files w ON w.project_id = c.project_id AND w.path = f.path AND w.hash = f.hash AND w.worktree = ?"
		args = append(args, worktree)
	}

	if topK <= 0 {
		topK = -1 // SQLite treats LIMIT -1 as "no limit"
	}

	// #nosec G201 -- join is one of two constant literals; all values are bound as ? params.
	// Note: FTS5 MATCH requires the table name, not an alias.
	query := fmt.Sprintf(`
		SELECT f.path, c.content, c.start_line, c.end_line, 0.5 AS score
		FROM chunks c
		JOIN chunks_fts ft ON ft.rowid = c.id
		%s
		WHERE chunks_fts MATCH ? AND c.project_id = ?
		LIMIT ?
	`, join)

	args = append(args, ftsQuery, projectID, topK)

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

// InsertFileDependencies replaces import/dependency edges for a source file.
func (s *SQLiteStore) InsertFileDependencies(ctx context.Context, projectID int, sourceFile string, targets []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM file_dependencies WHERE project_id = ? AND source_file = ?
	`, projectID, sourceFile); err != nil {
		return err
	}
	if len(targets) == 0 {
		return tx.Commit()
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO file_dependencies (project_id, source_file, target_file)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, target := range targets {
		if _, err := stmt.ExecContext(ctx, projectID, sourceFile, target); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FetchGraphNeighbors returns the full dependency graph for a project as
// source_file -> [target_file, ...] pairs. Returns an empty map if no
// edges exist.
func (s *SQLiteStore) FetchGraphNeighbors(ctx context.Context, projectID int) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_file, target_file FROM file_dependencies WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string][]string)
	for rows.Next() {
		var source, target string
		if err := rows.Scan(&source, &target); err != nil {
			return nil, err
		}
		result[source] = append(result[source], target)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// FetchChunksByPath returns chunks for a specific file path, ordered by
// chunk_index. Returns an empty slice if the file has no chunks.
func (s *SQLiteStore) FetchChunksByPath(ctx context.Context, projectID int, filePath string, dims, limit int) ([]store.SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.path, c.content, c.start_line, c.end_line, 0.5 AS score FROM chunks c JOIN files f ON f.id = c.file_id WHERE c.project_id = ? AND f.path = ? ORDER BY c.chunk_index LIMIT ?`, projectID, filePath, limit)
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

// FetchChunksByDirPrefix returns chunks for files whose path starts with the
// given directory prefix. Returns empty slice if no files match.
func (s *SQLiteStore) FetchChunksByDirPrefix(ctx context.Context, projectID int, dirPrefix string, dims, limit int) ([]store.SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.path, c.content, c.start_line, c.end_line, 0.5 AS score FROM chunks c JOIN files f ON f.id = c.file_id WHERE c.project_id = ? AND f.path LIKE (? || '%') ORDER BY c.chunk_index LIMIT ?`, projectID, dirPrefix, limit)
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
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	for _, stmt := range []string{
		`DELETE FROM chunks`,
		`DELETE FROM chunks_fts`,
		`DELETE FROM file_dependencies`,
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

// ExportedChunk is one stored chunk with its file metadata and raw embedding —
// enough to re-insert into another store without re-embedding.
type ExportedChunk struct {
	FilePath  string
	FileHash  string
	FileSize  int
	Content   string
	Embedding []float32 // nil for text-only (sensitive) chunks
	Dims      int
	StartLine int
	EndLine   int
}

// ExportChunks returns every stored chunk of a project (file metadata + decoded
// embedding), ordered by file, dims, then chunk index, so `semidx migrate` can
// copy the index into Postgres without recomputing embeddings.
func (s *SQLiteStore) ExportChunks(ctx context.Context, projectID int) ([]ExportedChunk, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.path, f.hash, f.size_bytes, c.content, c.embedding, c.dims, c.start_line, c.end_line
		FROM chunks c JOIN files f ON f.id = c.file_id
		WHERE c.project_id = ?
		ORDER BY f.path, c.dims, c.chunk_index
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ExportedChunk
	for rows.Next() {
		var e ExportedChunk
		var blob []byte
		if err := rows.Scan(&e.FilePath, &e.FileHash, &e.FileSize, &e.Content, &blob, &e.Dims, &e.StartLine, &e.EndLine); err != nil {
			return nil, err
		}
		if len(blob) > 0 {
			e.Embedding = decodeEmbedding(blob)
		}
		out = append(out, e)
	}
	return out, rows.Err()
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

// siftDown is the min-heap sift-down operation used by the top-K selection in
// searchSimilar. It maintains the heap invariant: parent <= children.
func siftDown(items []store.SearchResult, i, n int) {
	for {
		smallest := i
		left := 2*i + 1
		right := 2*i + 2
		if left < n && items[left].Score < items[smallest].Score {
			smallest = left
		}
		if right < n && items[right].Score < items[smallest].Score {
			smallest = right
		}
		if smallest == i {
			break
		}
		items[i], items[smallest] = items[smallest], items[i]
		i = smallest
	}
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
