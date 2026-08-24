// Package localstore is a standalone, PostgreSQL-free implementation of
// store.IndexStore backed by a single pure-Go SQLite file (modernc.org/sqlite,
// no CGO). It lets the CLI index and search a project entirely locally — the
// on-disk index typically lives at ~/.local/share/semidx/index.db.
//
// Embeddings are stored as little-endian float32 BLOBs and similarity search is
// a brute-force cosine scan over a project's chunks. That is O(n) per query,
// which is fine at laptop/single-repo scale (tens of thousands of chunks); the
// FTS5/BM25 keyword path stays indexed for fast lexical fallback; the server
// path (PgStore + pgvector HNSW) remains the choice for large vector corpora.
package localstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	sqlite "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/keyword"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/tenant"
)

// SQLiteStore implements store.IndexStore over a local SQLite database.
type SQLiteStore struct {
	db *sql.DB

	fillMu    sync.Mutex
	fillStmts map[int]*sql.Stmt // cached "content by id IN (…N…)" statements
}

// compile-time assertion that SQLiteStore satisfies the indexing/search subset.
var _ store.IndexStore = (*SQLiteStore)(nil)

// projectColumns is the canonical projection order shared by the project
// getters so scanProject can read any of them.
const projectColumns = `id, name, path, model, status, source_type, git_url, branch, COALESCE(identity, ''), COALESCE(dims, 0), COALESCE(license_spdx_id, ''), COALESCE(privacy_mode, 'hybrid')`

// schema mirrors the pgvector layout conceptually as plain tables: an embedding
// BLOB replaces the vector column and there is one chunks table instead of the
// per-dimension chunks_<dims> tables (SQLite is dynamically typed, so a single
// table holds vectors of any dimension).
const schema = `
CREATE TABLE IF NOT EXISTS projects (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    path            TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT '',
    source_type     TEXT NOT NULL DEFAULT 'path',
    git_url         TEXT NOT NULL DEFAULT '',
    branch          TEXT NOT NULL DEFAULT '',
    identity        TEXT,
    dims            INTEGER NOT NULL DEFAULT 0,
    license_spdx_id TEXT NOT NULL DEFAULT '',
    privacy_mode    TEXT NOT NULL DEFAULT 'hybrid',
    last_indexed_commit TEXT NOT NULL DEFAULT ''
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
CREATE INDEX IF NOT EXISTS idx_file_deps_target ON file_dependencies(project_id, target_file);
CREATE TABLE IF NOT EXISTS project_dependencies (
    tenant_id        INTEGER NOT NULL DEFAULT 1,
    project_id       INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    ecosystem        TEXT NOT NULL,
    name             TEXT NOT NULL,
    normalized_name  TEXT NOT NULL,
    constraint_text  TEXT NOT NULL DEFAULT '',
    resolved_version TEXT NOT NULL DEFAULT '',
    scope            TEXT NOT NULL DEFAULT 'runtime',
    source           TEXT NOT NULL DEFAULT '',
    manifest         TEXT NOT NULL DEFAULT '',
    direct           INTEGER NOT NULL DEFAULT 1,
    observed_at     TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (tenant_id, project_id, ecosystem, normalized_name, scope)
);
CREATE INDEX IF NOT EXISTS idx_project_dependencies_lookup
    ON project_dependencies (tenant_id, ecosystem, normalized_name);
CREATE TABLE IF NOT EXISTS runtime_edges (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id         INTEGER NOT NULL DEFAULT 1,
    workspace_id      INTEGER NOT NULL DEFAULT 0,
    source_project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    target_project_id INTEGER NOT NULL DEFAULT 0,
    target_name       TEXT NOT NULL,
    source_component  TEXT NOT NULL DEFAULT '',
    target_component  TEXT NOT NULL DEFAULT '',
    protocol          TEXT NOT NULL DEFAULT '',
    environment       TEXT NOT NULL DEFAULT '',
    request_count     INTEGER NOT NULL DEFAULT 0,
    error_count       INTEGER NOT NULL DEFAULT 0,
    p95_latency_ms    REAL NOT NULL DEFAULT 0,
    first_seen        TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen         TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (tenant_id, workspace_id, source_project_id, target_project_id,
        target_name, source_component, target_component, protocol, environment)
);
CREATE INDEX IF NOT EXISTS idx_runtime_edges_workspace
    ON runtime_edges (tenant_id, workspace_id, source_project_id, last_seen);
CREATE TABLE IF NOT EXISTS tenant_quotas (
    tenant_id         INTEGER PRIMARY KEY,
    plan              TEXT NOT NULL DEFAULT 'free',
    max_projects      INTEGER NOT NULL DEFAULT 0,
    max_runtime_edges INTEGER NOT NULL DEFAULT 0,
    updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT OR IGNORE INTO tenant_quotas (tenant_id) VALUES (1);
CREATE TABLE IF NOT EXISTS chunks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    file_id     INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    content     TEXT NOT NULL,
    -- Storage de-duplication (ADR-7): new rows reference unique_embeddings by
    -- emb_hash and leave embedding NULL; legacy rows keep the inline embedding.
    embedding   BLOB,
    emb_hash    TEXT,
    dims        INTEGER NOT NULL DEFAULT 0,
    model       TEXT NOT NULL DEFAULT '',
    start_line  INTEGER,
    end_line    INTEGER,
    confidence  TEXT NOT NULL DEFAULT 'AMBIGUOUS',
    symbol      TEXT,
    UNIQUE(project_id, file_id, chunk_index)
);
-- unique_embeddings de-duplicates identical vectors (same content across
-- worktrees/subprojects embeds to the same vector) so each is stored once.
-- Keyed by the SHA-256 of the encoded vector, so no interface change is needed.
CREATE TABLE IF NOT EXISTS unique_embeddings (
    emb_hash   TEXT PRIMARY KEY,
    embedding  BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS embedding_cache (
    input_hash  TEXT NOT NULL,
    model       TEXT NOT NULL,
    dims        INTEGER NOT NULL,
    embedding   BLOB NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (input_hash, model)
);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_lookup ON embedding_cache(input_hash, model);
CREATE TABLE IF NOT EXISTS usage_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          TEXT NOT NULL DEFAULT (datetime('now')),
    project     TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'unknown',
    outcome     TEXT NOT NULL DEFAULT 'ok',
    hit_count   INTEGER NOT NULL DEFAULT 0,
    latency_ms  INTEGER NOT NULL DEFAULT 0,
    keyword     INTEGER NOT NULL DEFAULT 0,
    graph       INTEGER NOT NULL DEFAULT 0,
    query_hash  TEXT NOT NULL DEFAULT '',
    query_text  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_usage_events_ts ON usage_events(ts DESC);
CREATE INDEX IF NOT EXISTS idx_usage_events_project_ts ON usage_events(project, ts DESC);
CREATE INDEX IF NOT EXISTS idx_files_project ON files(project_id);
CREATE INDEX IF NOT EXISTS idx_chunks_project ON chunks(project_id);
CREATE INDEX IF NOT EXISTS idx_chunks_file ON chunks(file_id);

-- Persistent chat conversations (store.ConversationStore). The local binary is
-- single-user, so callers pass user_id=0; the column still scopes rows so the
-- Postgres semantics (owner isolation) hold. Timestamps carry millisecond
-- precision so the recency ordering of ListConversations is stable.
CREATE TABLE IF NOT EXISTS conversations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL DEFAULT 0,
    project    TEXT NOT NULL DEFAULT '',
    title      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations(user_id, updated_at);
CREATE TABLE IF NOT EXISTS conversation_messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    sources_json    TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX IF NOT EXISTS idx_conversation_messages_conv ON conversation_messages(conversation_id);

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
	lockPath := filepath.Clean(schemaLockPath(path))
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
		var hasLastIndexedCommit int
		_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name='last_indexed_commit'`).Scan(&hasLastIndexedCommit)
		var ddl string
		_ = db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='projects'`).Scan(&ddl)
		// Rebuild an older DB: missing the 'identity' column (pre-F11), missing the
		// 'last_indexed_commit' column (recent), OR still enforcing UNIQUE on the
		// projects table — i.e. UNIQUE(name) (pre-F14), which wrongly blocks two
		// projects that share a basename.
		if hasIdentity == 0 || hasLastIndexedCommit == 0 || strings.Contains(strings.ToUpper(ddl), "UNIQUE") {
			for _, tbl := range []string{"chunks_fts", "chunks", "worktree_files", "files", "projects"} {
				if _, err := db.Exec("DROP TABLE IF EXISTS " + tbl); err != nil {
					return err
				}
			}
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	var hasPrivacyMode int
	_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name='privacy_mode'`).Scan(&hasPrivacyMode)
	if hasPrivacyMode == 0 {
		if _, err := db.Exec(`ALTER TABLE projects ADD COLUMN privacy_mode TEXT NOT NULL DEFAULT 'hybrid'`); err != nil {
			return err
		}
	}
	// Add the emb_hash column to a pre-ADR-7 chunks table (CREATE TABLE IF NOT
	// EXISTS won't alter an existing one). SQLite has no ADD COLUMN IF NOT
	// EXISTS, so guard on pragma_table_info.
	var hasEmbHash int
	_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('chunks') WHERE name='emb_hash'`).Scan(&hasEmbHash)
	if hasEmbHash == 0 {
		if _, err := db.Exec(`ALTER TABLE chunks ADD COLUMN emb_hash TEXT`); err != nil {
			return err
		}
	}
	var hasConfidence int
	_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('chunks') WHERE name='confidence'`).Scan(&hasConfidence)
	if hasConfidence == 0 {
		if _, err := db.Exec(`ALTER TABLE chunks ADD COLUMN confidence TEXT NOT NULL DEFAULT 'AMBIGUOUS'`); err != nil {
			return err
		}
	}
	var hasSymbol int
	_ = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('chunks') WHERE name='symbol'`).Scan(&hasSymbol)
	if hasSymbol == 0 {
		if _, err := db.Exec(`ALTER TABLE chunks ADD COLUMN symbol TEXT`); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) Close() { _ = s.db.Close() }

// Ping verifies the database is reachable (used by /readyz parity).
func (s *SQLiteStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// EnsureChunksTable is a no-op: unlike pgvector there is a single, schemaless
// chunks table created on open, so there is no per-dimension table to build.
func (s *SQLiteStore) EnsureChunksTable(_ context.Context, _ int) error { return nil }

// EnsureEmbeddingCacheTable is a no-op: the embedding_cache table is part of
// the static schema created on open, so there is no runtime DDL to perform.
func (s *SQLiteStore) EnsureEmbeddingCacheTable(_ context.Context, _ int) error { return nil }

// LookupEmbeddingCache fetches cached embeddings for the given input hashes
// and model. Returns a map of hash→embedding; hashes not found in the cache
// are absent from the map.
func (s *SQLiteStore) LookupEmbeddingCache(ctx context.Context, inputHashes []string, model string, _ int) (map[string][]float32, error) {
	if len(inputHashes) == 0 {
		return map[string][]float32{}, nil
	}
	if len(inputHashes) > 990 {
		return nil, fmt.Errorf("too many input hashes: %d (max 990)", len(inputHashes))
	}

	// Build dynamic IN clause: (?, ?, ..., ?)
	placeholders := make([]string, len(inputHashes))
	args := make([]any, 0, len(inputHashes)+1)
	for i, h := range inputHashes {
		placeholders[i] = "?"
		args = append(args, h)
	}
	args = append(args, model)

	// #nosec G202 -- placeholders are always literal "?" strings, not user input.
	query := `SELECT input_hash, embedding FROM embedding_cache WHERE input_hash IN (` +
		strings.Join(placeholders, ",") + `) AND model = ? LIMIT ?`
	args = append(args, len(inputHashes))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string][]float32, len(inputHashes))
	for rows.Next() {
		var inputHash string
		var blob []byte
		if err := rows.Scan(&inputHash, &blob); err != nil {
			return nil, err
		}
		result[inputHash] = decodeEmbedding(blob)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// InsertEmbeddingCache stores embeddings in the cache table. Uses
// ON CONFLICT(input_hash, model) DO NOTHING for concurrent safety.
func (s *SQLiteStore) InsertEmbeddingCache(ctx context.Context, inputHashes []string, model string, embeddings [][]float32, dims int) error {
	if len(inputHashes) != len(embeddings) {
		return fmt.Errorf("inputHashes and embeddings length mismatch: %d vs %d", len(inputHashes), len(embeddings))
	}
	if len(inputHashes) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO embedding_cache (input_hash, model, dims, embedding)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(input_hash, model) DO NOTHING
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for i, hash := range inputHashes {
		if _, err := stmt.ExecContext(ctx, hash, model, dims, encodeEmbedding(embeddings[i])); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneEmbeddingCache removes all cache entries for the given dimension.
func (s *SQLiteStore) PruneEmbeddingCache(ctx context.Context, dims int) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM embedding_cache WHERE dims = ?`, dims)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneOrphanEmbeddings garbage-collects unique_embeddings rows that no chunk
// references (ADR-7 RF05). Rows are orphaned when a project or file (and its
// chunks) is deleted; the shared dictionary is never cascade-deleted (RNF02),
// so orphans accumulate until swept here. Returns the number of rows removed.
func (s *SQLiteStore) PruneOrphanEmbeddings(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM unique_embeddings
		WHERE emb_hash NOT IN (SELECT emb_hash FROM chunks WHERE emb_hash IS NOT NULL)`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

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
		  AND path NOT LIKE 'git:%'
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
	return &store.Project{ID: id, TenantID: tenant.DefaultID, Name: name, Model: model, Status: "registered", SourceType: sourceType, GitURL: gitURL, Branch: branch, Identity: name, Dims: dims, PrivacyMode: "hybrid"}, nil
}

func scanProject(row interface{ Scan(...any) error }) (*store.Project, error) {
	var p store.Project
	if err := row.Scan(&p.ID, &p.Name, &p.Path, &p.Model, &p.Status, &p.SourceType, &p.GitURL, &p.Branch, &p.Identity, &p.Dims, &p.LicenseSPDXID, &p.PrivacyMode); err != nil {
		return nil, err
	}
	p.TenantID = tenant.DefaultID
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

// SetProjectPrivacy persists the local project policy. Standalone mode keeps
// the same contract as the server even though it has no tenant selector.
func (s *SQLiteStore) SetProjectPrivacy(ctx context.Context, projectID int, mode string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "cloud" && mode != "hybrid" && mode != "edge" {
		return fmt.Errorf("invalid privacy mode %q", mode)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET privacy_mode = ? WHERE id = ?`, mode, projectID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return store.ErrNotFound
	}
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

// CountProjectFiles returns the total number of indexed files for a project.
func (s *SQLiteStore) CountProjectFiles(ctx context.Context, projectID int) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE project_id = ?`, projectID).Scan(&n)
	return n, err
}

// ListFileHashes returns path→hash for every indexed file of a project.
func (s *SQLiteStore) ListFileHashes(ctx context.Context, projectID int) (map[string]string, error) {
	infos, err := s.ListFileHashesWithTime(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(infos))
	for path, info := range infos {
		out[path] = info.Hash
	}
	return out, nil
}

// ListFileHashesWithTime returns path→(hash, indexed_at) for every indexed file
// of a project (used by search to flag stale previews).
func (s *SQLiteStore) ListFileHashesWithTime(ctx context.Context, projectID int) (map[string]store.FileHashInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, hash, indexed_at FROM files WHERE project_id = ? ORDER BY path, indexed_at DESC, id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]store.FileHashInfo)
	for rows.Next() {
		var path, hash, indexedAt string
		if err := rows.Scan(&path, &hash, &indexedAt); err != nil {
			return nil, err
		}
		if _, exists := out[path]; !exists {
			out[path] = store.FileHashInfo{Hash: hash, IndexedAt: parseSQLiteTime(indexedAt)}
		}
	}
	return out, rows.Err()
}

// PruneFileVersions removes older content-addressed versions of one canonical
// push path after the new version has been fully indexed.
func (s *SQLiteStore) PruneFileVersions(ctx context.Context, projectID int, path, keepHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM files
		WHERE project_id = ? AND path = ? AND hash <> ?`, projectID, path, keepHash)
	return err
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

// DeleteFileByID removes exactly one file row (by primary key); its chunks are
// removed via ON DELETE CASCADE. Other hash versions of the same path are
// untouched, so a single incompletely-indexed version can be rolled back while
// preserving older searchable ones.
func (s *SQLiteStore) DeleteFileByID(ctx context.Context, projectID, fileID int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE project_id = ? AND id = ?`, projectID, fileID)
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
		INSERT INTO chunks (project_id, file_id, chunk_index, content, emb_hash, dims, model, start_line, end_line, confidence, symbol)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, file_id, chunk_index) DO UPDATE
		SET content = excluded.content, embedding = NULL, emb_hash = excluded.emb_hash, dims = excluded.dims,
		    model = excluded.model, start_line = excluded.start_line, end_line = excluded.end_line,
		    confidence = excluded.confidence, symbol = excluded.symbol
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	dictStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO unique_embeddings (emb_hash, embedding) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = dictStmt.Close() }()

	var blob []byte // reused across rows to avoid per-chunk allocation
	for i, chunk := range chunks {
		var embHash any // NULL when text-only
		if embeddings != nil {
			// De-duplicate by the vector's own content hash: identical content
			// embeds to an identical vector, so it is stored once (ADR-7).
			blob = encodeEmbeddingInto(embeddings[i], blob)
			h := hashEmbedding(blob)
			if _, err := dictStmt.ExecContext(ctx, h, blob); err != nil {
				return err
			}
			embHash = h
		}
		conf := chunk.Confidence
		if conf == "" {
			conf = "AMBIGUOUS"
		}
		var symVal *string
		if chunk.Symbol != "" {
			symVal = &chunk.Symbol
		}
		if _, err := stmt.ExecContext(ctx, projectID, fileID, i, chunk.Content, embHash, dims, model, chunk.StartLine, chunk.EndLine, conf, symVal); err != nil {
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
	// COALESCE resolves the vector from the de-dup dictionary (new rows) or the
	// legacy inline column (pre-ADR-7 rows); rows with neither are text-only and
	// excluded so they surface via keyword search instead.
	// Perf note: chunk content is deliberately NOT selected here — scoring only
	// needs the embedding blob. Content is fetched for just the top-K rows in a
	// second query, avoiding a full-table content transfer per search.
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, f.path, c.start_line, c.end_line, COALESCE(ue.embedding, c.embedding), c.confidence, c.symbol
		FROM chunks c `+join+`
		LEFT JOIN unique_embeddings ue ON ue.emb_hash = c.emb_hash
		WHERE c.project_id = ? AND COALESCE(ue.embedding, c.embedding) IS NOT NULL
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	// Stream rows into a fixed-size min-heap so we never materialise all chunks
	// in memory (REQ-STOR-04). When topK<=0, accumulate everything.
	// RawBytes avoids database/sql cloning the driver's buffer per row; the
	// bytes are consumed before the next rows.Next() call.
	var (
		top     []similarHit
		all     []similarHit
		useHeap = topK > 0
		blob    sql.RawBytes
		qNorm   = vectorNorm(embedding)
	)
	for rows.Next() {
		var (
			hit        similarHit
			startLine  sql.NullInt64
			endLine    sql.NullInt64
			confidence sql.NullString
			symbol     sql.NullString
		)
		if err := rows.Scan(&hit.id, &hit.res.FilePath, &startLine, &endLine, &blob, &confidence, &symbol); err != nil {
			return nil, err
		}
		hit.res.StartLine = int(startLine.Int64)
		hit.res.EndLine = int(endLine.Int64)
		hit.res.Score = cosineWithQueryNormFromBlob(embedding, qNorm, blob)
		hit.res.Confidence = confidence.String
		if hit.res.Confidence == "" {
			hit.res.Confidence = "AMBIGUOUS"
		}
		hit.res.Symbol = symbol.String
		if !useHeap {
			all = append(all, hit)
			continue
		}
		top = pushSimilarHitTopK(top, hit, topK)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !useHeap {
		results := make([]store.SearchResult, len(all))
		for i, h := range all {
			results[i] = h.res
		}
		if err := s.fillChunkContents(ctx, all, results); err != nil {
			return nil, err
		}
		sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
		return results, nil
	}
	if len(top) == 0 {
		return nil, nil
	}
	results := make([]store.SearchResult, len(top))
	for i, h := range top {
		results[i] = h.res
	}
	if err := s.fillChunkContents(ctx, top, results); err != nil {
		return nil, err
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results, nil
}

// similarHit is a scored candidate kept during the streaming scan; content is
// resolved later via fillChunkContents so the hot loop never copies it.
type similarHit struct {
	id  int64
	res store.SearchResult
}

// fillChunkContents populates Content for each result in one batched query.
// The IN-list statement is prepared once per arity and reused: re-preparing it
// on every search showed up as a measurable latency regression.
func (s *SQLiteStore) fillChunkContents(ctx context.Context, hits []similarHit, results []store.SearchResult) error {
	if len(hits) == 0 {
		return nil
	}
	ids := make([]any, len(hits))
	byID := make(map[int64]int, len(hits))
	for i, h := range hits {
		ids[i] = h.id
		byID[h.id] = i
	}
	stmt, err := s.fillStmt(len(ids))
	if err != nil {
		return err
	}
	rows, err := stmt.QueryContext(ctx, ids...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id      int64
			content string
		)
		if err := rows.Scan(&id, &content); err != nil {
			return err
		}
		if idx, ok := byID[id]; ok {
			results[idx].Content = content
		}
	}
	return rows.Err()
}

// fillStmt returns a cached prepared statement selecting content for N chunk
// ids. Callers must not close it; the store owns the lifecycle.
func (s *SQLiteStore) fillStmt(n int) (*sql.Stmt, error) {
	s.fillMu.Lock()
	defer s.fillMu.Unlock()
	if st, ok := s.fillStmts[n]; ok {
		return st, nil
	}
	marks := make([]string, n)
	for i := range marks {
		marks[i] = "?"
	}
	// marks contains only generated "?" placeholders — no user input reaches the
	// SQL text; the count varies with result size.
	// #nosec G202 -- placeholders are generated literals, not user input.
	st, err := s.db.Prepare(`SELECT id, content FROM chunks WHERE id IN (` + strings.Join(marks, ",") + `)`)
	if err != nil {
		return nil, err
	}
	if s.fillStmts == nil {
		s.fillStmts = make(map[int]*sql.Stmt)
	}
	s.fillStmts[n] = st
	return st, nil
}

// pushSimilarHitTopK maintains a min-heap of the top-K cosine scores over
// scored candidates that defer content loading.
func pushSimilarHitTopK(top []similarHit, r similarHit, topK int) []similarHit {
	if len(top) < topK {
		top = append(top, r)
		if len(top) == topK {
			// Heapify once when the heap first fills.
			n := len(top)
			for i := n/2 - 1; i >= 0; i-- {
				siftDownHits(top, i, n)
			}
		}
		return top
	}
	if r.res.Score <= top[0].res.Score {
		return top
	}
	top[0] = r
	siftDownHits(top, 0, len(top))
	return top
}

// siftDownHits is siftDown over similarHit slices.
func siftDownHits(items []similarHit, i, n int) {
	for {
		smallest := i
		left := 2*i + 1
		right := 2*i + 2
		if left < n && items[left].res.Score < items[smallest].res.Score {
			smallest = left
		}
		if right < n && items[right].res.Score < items[smallest].res.Score {
			smallest = right
		}
		if smallest == i {
			break
		}
		items[i], items[smallest] = items[smallest], items[i]
		i = smallest
	}
}

// SearchSimilarKeywords finds chunks whose content matches any query word via
// SQLite FTS5. The score is the monotonic positive form of FTS5's BM25 rank
// (FTS5 returns lower, usually negative, values for better matches), so callers
// can rely on keyword results being ordered by lexical relevance.
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

	if topK <= 0 {
		topK = -1 // SQLite treats LIMIT -1 as "no limit"
	}

	// Note: FTS5 MATCH requires the table name, not an alias. The optional
	// worktree scope is expressed with EXISTS so the query remains static and
	// all user-controlled values stay parameters.
	const ftsSQL = `
		SELECT f.path, c.content, c.start_line, c.end_line, -bm25(chunks_fts) AS score, c.confidence, c.symbol
		FROM chunks c
		JOIN chunks_fts ft ON ft.rowid = c.id
		JOIN files f ON f.id = c.file_id
		WHERE chunks_fts MATCH ? AND c.project_id = ?
		  AND (? = '' OR EXISTS (
			SELECT 1 FROM worktree_files w
			WHERE w.project_id = c.project_id AND w.path = f.path
			  AND w.hash = f.hash AND w.worktree = ?
		  ))
		ORDER BY bm25(chunks_fts) ASC, c.id ASC
		LIMIT ?
	`

	ftsArgs := []any{ftsQuery, projectID, worktree, worktree, topK}

	rows, err := s.db.QueryContext(ctx, ftsSQL, ftsArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ftsResults, err := scanChunkResultRows(rows)
	if err != nil {
		return nil, err
	}
	if topK > 0 && len(ftsResults) >= topK {
		return ftsResults, nil
	}

	// FTS5 cannot combine MATCH with an OR predicate in one WHERE clause. Run
	// the compatibility substring leg separately and merge it after the ranked
	// FTS results. This keeps camelCase/path matches available on SQLite while
	// ensuring token-ranked matches always win ties.
	const likeQuery = `
		SELECT f.path, c.content, c.start_line, c.end_line, 0.0 AS score, c.confidence, c.symbol
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		WHERE c.project_id = ?
		  AND (? = '' OR EXISTS (
			SELECT 1 FROM worktree_files w
			WHERE w.project_id = c.project_id AND w.path = f.path
			  AND w.hash = f.hash AND w.worktree = ?
		  ))
		  AND (
			c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ?
			OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ?
			OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ?
			OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ? OR c.content LIKE ?
		  )
		ORDER BY c.id ASC
		LIMIT ?
	`
	likeArgs := []any{projectID, worktree, worktree}
	for i := 0; i < 20; i++ {
		if i < len(words) {
			likeArgs = append(likeArgs, "%"+words[i]+"%")
		} else {
			likeArgs = append(likeArgs, nil)
		}
	}
	likeArgs = append(likeArgs, topK)
	likeRows, err := s.db.QueryContext(ctx, likeQuery, likeArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = likeRows.Close() }()
	likeResults, err := scanChunkResultRows(likeRows)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(ftsResults)+len(likeResults))
	for _, result := range ftsResults {
		seen[keywordResultKey(result)] = struct{}{}
	}
	merged := append([]store.SearchResult{}, ftsResults...)
	for _, result := range likeResults {
		if _, ok := seen[keywordResultKey(result)]; ok {
			continue
		}
		seen[keywordResultKey(result)] = struct{}{}
		merged = append(merged, result)
	}
	if topK > 0 && len(merged) > topK {
		merged = merged[:topK]
	}
	return merged, nil
}

func keywordResultKey(result store.SearchResult) string {
	return fmt.Sprintf("%s\x00%d\x00%d\x00%s", result.FilePath, result.StartLine, result.EndLine, result.Content)
}

// scanChunkResultRows reads the canonical "path, content, start_line, end_line,
// score, confidence, symbol" row shape shared by keyword search and the
// FetchChunksBy{Path,DirPrefix} helpers. Empty confidence is normalized to
// AMBIGUOUS (the column default) so callers see a stable tag for pre-v2 rows.
// The caller owns closing the rows.
func scanChunkResultRows(rows *sql.Rows) ([]store.SearchResult, error) {
	var results []store.SearchResult
	for rows.Next() {
		var (
			r          store.SearchResult
			startLine  sql.NullInt64
			endLine    sql.NullInt64
			confidence sql.NullString
			symbol     sql.NullString
		)
		if err := rows.Scan(&r.FilePath, &r.Content, &startLine, &endLine, &r.Score, &confidence, &symbol); err != nil {
			return nil, err
		}
		r.StartLine = int(startLine.Int64)
		r.EndLine = int(endLine.Int64)
		r.Confidence = confidence.String
		if r.Confidence == "" {
			r.Confidence = "AMBIGUOUS"
		}
		r.Symbol = symbol.String
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
	rows, err := s.db.QueryContext(ctx, `SELECT f.path, c.content, c.start_line, c.end_line, 0.5 AS score, c.confidence, c.symbol FROM chunks c JOIN files f ON f.id = c.file_id WHERE c.project_id = ? AND f.path = ? ORDER BY c.chunk_index LIMIT ?`, projectID, filePath, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanChunkResultRows(rows)
}

// FetchChunksByDirPrefix returns chunks for files whose path starts with the
// given directory prefix. Returns empty slice if no files match.
func (s *SQLiteStore) FetchChunksByDirPrefix(ctx context.Context, projectID int, dirPrefix string, dims, limit int) ([]store.SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.path, c.content, c.start_line, c.end_line, 0.5 AS score, c.confidence, c.symbol FROM chunks c JOIN files f ON f.id = c.file_id WHERE c.project_id = ? AND f.path LIKE (? || '%') ORDER BY c.chunk_index LIMIT ?`, projectID, dirPrefix, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanChunkResultRows(rows)
}

// FetchChunksByPaths implements store.GraphChunkBatchStore: one query for the
// whole set of graph-discovered files instead of one query per file. The
// per-file limit is applied by a window function so a single hot file cannot
// crowd out the rest.
func (s *SQLiteStore) FetchChunksByPaths(ctx context.Context, projectID int, paths []string, dims, limitPerPath int) (map[string][]store.SearchResult, error) {
	if len(paths) == 0 || limitPerPath <= 0 {
		return nil, nil
	}
	// The path set travels as a single JSON parameter expanded by json_each, so
	// the statement is a constant: no placeholder list is built by hand, and
	// every path stays a bound value.
	pathsJSON, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	const query = `SELECT path, content, start_line, end_line, score, confidence, symbol FROM (
			SELECT f.path AS path, c.content, c.start_line, c.end_line, 0.5 AS score,
			       c.confidence, c.symbol,
			       ROW_NUMBER() OVER (PARTITION BY f.path ORDER BY c.chunk_index) AS rn
			FROM chunks c
			JOIN files f ON f.id = c.file_id
			WHERE c.project_id = ? AND f.path IN (SELECT value FROM json_each(?))
		) WHERE rn <= ? ORDER BY path, start_line`
	rows, err := s.db.QueryContext(ctx, query, projectID, string(pathsJSON), limitPerPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	flat, err := scanChunkResultRows(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]store.SearchResult, len(paths))
	for _, chunk := range flat {
		out[chunk.FilePath] = append(out[chunk.FilePath], chunk)
	}
	return out, nil
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
		`DELETE FROM embedding_cache`,
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
		SELECT f.path, f.hash, f.size_bytes, c.content,
		       COALESCE(ue.embedding, c.embedding), c.dims, c.start_line, c.end_line
		FROM chunks c JOIN files f ON f.id = c.file_id
		LEFT JOIN unique_embeddings ue ON ue.emb_hash = c.emb_hash
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
	return encodeEmbeddingInto(vec, nil)
}

// encodeEmbeddingInto serializes into buf (grown as needed) so hot loops can
// reuse a scratch buffer instead of allocating per row.
func encodeEmbeddingInto(vec []float32, buf []byte) []byte {
	n := len(vec) * 4
	if cap(buf) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// hashEmbedding returns the hex SHA-256 of an encoded vector without the
// reflection cost of fmt.Sprintf("%x", …).
func hashEmbedding(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

// decodeEmbedding reverses encodeEmbedding.
func decodeEmbedding(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	return decodeEmbeddingInto(b, out)
}

// decodeEmbeddingInto decodes an embedding blob into buf (grown as needed) and
// returns it, so hot loops can reuse one scratch buffer instead of allocating
// per row.
func decodeEmbeddingInto(b []byte, buf []float32) []float32 {
	n := len(b) / 4
	if cap(buf) < n {
		buf = make([]float32, n)
	}
	buf = buf[:n]
	for i := 0; i < n; i++ {
		buf[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return buf
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

// vectorNorm returns the L2 norm of v, or 0 for an empty vector.
func vectorNorm(v []float32) float64 {
	var sum float64
	for _, f := range v {
		fa := float64(f)
		sum += fa * fa
	}
	return math.Sqrt(sum)
}

// cosineWithQueryNormFromBlob computes cosine(query, candidate) reusing a
// pre-computed ||query|| so the per-candidate cost drops by a third during
// brute-force scans, reading the candidate straight off its encoded
// little-endian float32 blob (no per-row []float32 materialisation). Falls back
// to 0 on length mismatch or zero vectors, mirroring cosineSimilarity.
func cosineWithQueryNormFromBlob(a []float32, normA float64, blob []byte) float64 {
	n := len(blob) / 4
	if len(a) != n || len(a) == 0 {
		return 0
	}
	var dot, normB float64
	for i := 0; i < n; i++ {
		fb := float64(math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:])))
		fa := float64(a[i])
		dot += fa * fb
		normB += fb * fb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normB) * normA)
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

func (s *SQLiteStore) UpdateProjectCommit(ctx context.Context, projectID int, commitSHA string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET last_indexed_commit = ? WHERE id = ?`, commitSHA, projectID)
	return err
}

func (s *SQLiteStore) GetProjectCommit(ctx context.Context, projectID int) (string, error) {
	var sha string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(last_indexed_commit, '') FROM projects WHERE id = ?`, projectID).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return sha, err
}

func (s *SQLiteStore) FetchGraphPathsBFS(ctx context.Context, projectID int, seedPaths []string, maxDepth int) (map[string]int, error) {
	if len(seedPaths) == 0 || maxDepth <= 0 {
		return nil, nil
	}

	// Build a set of seed paths for post-query filtering.
	seedSet := make(map[string]struct{}, len(seedPaths))
	for _, p := range seedPaths {
		seedSet[p] = struct{}{}
	}

	// SQLite's ? placeholders are positional across UNION branches, so we must
	// repeat values. We build a flat arg list: seeds, projectID, seeds, projectID,
	// projectID, maxDepth, projectID, maxDepth.
	// Use json_each to avoid dynamic IN clause construction (G202/G201).
	seedJSON, _ := json.Marshal(seedPaths)

	var args = []any{string(seedJSON), projectID, string(seedJSON), projectID, projectID, maxDepth, projectID, maxDepth}

	query := `WITH RECURSIVE graph_bfs(file_path, depth) AS (
		SELECT fd.target_file, 1
		FROM file_dependencies fd
		WHERE fd.source_file IN (SELECT value FROM json_each(?)) AND fd.project_id = ?

		UNION

		SELECT fd.source_file, 1
		FROM file_dependencies fd
		WHERE fd.target_file IN (SELECT value FROM json_each(?)) AND fd.project_id = ?

		UNION

		SELECT fd.target_file, g.depth + 1
		FROM file_dependencies fd
		JOIN graph_bfs g ON fd.source_file = g.file_path
		WHERE fd.project_id = ? AND g.depth < ?

		UNION

		SELECT fd.source_file, g.depth + 1
		FROM file_dependencies fd
		JOIN graph_bfs g ON fd.target_file = g.file_path
		WHERE fd.project_id = ? AND g.depth < ?
	)
	SELECT file_path, MIN(depth) AS depth
	FROM graph_bfs
	GROUP BY file_path`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]int)
	for rows.Next() {
		var path string
		var depth int
		if err := rows.Scan(&path, &depth); err != nil {
			return nil, err
		}
		// Filter out seed paths so the caller gets only newly discovered files.
		if _, isSeed := seedSet[path]; !isSeed {
			result[path] = depth
		}
	}
	return result, rows.Err()
}

var _ store.DependencyStore = (*SQLiteStore)(nil)

func (s *SQLiteStore) ReplaceProjectDependencies(ctx context.Context, projectID int, deps []store.Dependency) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_dependencies WHERE tenant_id = ? AND project_id = ?`, tenant.ID(ctx), projectID); err != nil {
		return err
	}
	for _, dep := range deps {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO project_dependencies
			(tenant_id, project_id, ecosystem, name, normalized_name, constraint_text,
			 resolved_version, scope, source, manifest, direct)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (tenant_id, project_id, ecosystem, normalized_name, scope)
			DO UPDATE SET name=excluded.name, constraint_text=excluded.constraint_text,
			 resolved_version=excluded.resolved_version, source=excluded.source,
			 manifest=excluded.manifest, direct=excluded.direct, observed_at=datetime('now')
		`, tenant.ID(ctx), projectID, dep.Ecosystem, dep.Name, dep.NormalizedName,
			dep.Constraint, dep.ResolvedVersion, dep.Scope, dep.Source, dep.Manifest, dep.Direct)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListProjectDependencies(ctx context.Context, projectID int) ([]store.Dependency, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ecosystem, name, normalized_name, constraint_text, resolved_version,
			scope, source, manifest, direct
		FROM project_dependencies WHERE tenant_id = ? AND project_id = ?
		ORDER BY ecosystem, normalized_name, scope`, tenant.ID(ctx), projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []store.Dependency
	for rows.Next() {
		var dep store.Dependency
		if err := rows.Scan(&dep.Ecosystem, &dep.Name, &dep.NormalizedName, &dep.Constraint,
			&dep.ResolvedVersion, &dep.Scope, &dep.Source, &dep.Manifest, &dep.Direct); err != nil {
			return nil, err
		}
		out = append(out, dep)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) FindProjectsSharingDependency(ctx context.Context, projectID int) ([]store.DependencyUsage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT p2.id, d2.tenant_id, p2.name, d2.ecosystem, d2.name,
			d2.normalized_name, d2.constraint_text, d2.resolved_version, d2.scope, d2.direct
		FROM project_dependencies d1
		JOIN project_dependencies d2 ON d2.tenant_id = d1.tenant_id
		 AND d2.ecosystem = d1.ecosystem AND d2.normalized_name = d1.normalized_name
		JOIN projects p2 ON p2.id = d2.project_id
		WHERE d1.tenant_id = ? AND d1.project_id = ? AND d2.project_id <> ?
		ORDER BY p2.name, d2.ecosystem, d2.normalized_name`, tenant.ID(ctx), projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []store.DependencyUsage
	for rows.Next() {
		var usage store.DependencyUsage
		if err := rows.Scan(&usage.ProjectID, &usage.TenantID, &usage.ProjectName, &usage.Ecosystem,
			&usage.Name, &usage.NormalizedName, &usage.Constraint, &usage.ResolvedVersion,
			&usage.Scope, &usage.Direct); err != nil {
			return nil, err
		}
		out = append(out, usage)
	}
	return out, rows.Err()
}
