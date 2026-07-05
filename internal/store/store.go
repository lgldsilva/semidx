// Package store persists projects, files and per-dimension chunk vectors in
// PostgreSQL/pgvector. Callers depend on the Store interface, so the indexer and
// search layers can be unit-tested against a fake while PgStore is exercised by
// integration tests against a real pgvector container.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	SourceType string // "push" | "git" | "path" | "docs"
	GitURL     string // set when SourceType == "git"
	Branch     string // optional git branch
	Identity   string // stable dedup key (git remote/common-dir, or absolute path)
	Dims       int    // embedding dimension used for this project (0 = unknown/probe)
}

// Sentinel errors so callers (e.g. the HTTP layer) can map to status codes
// without depending on pgx internals.
var (
	ErrNotFound      = errors.New("not found")
	ErrProjectExists = errors.New("project already exists")
	ErrUserExists    = errors.New("user already exists")
)

// KeywordDims is the chunk-table dimension bucket used for keyword-only (no
// embedding) indexing. Text-only chunks carry a NULL embedding, so the concrete
// dimension is irrelevant — they all share this single small bucket.
const KeywordDims = 1

// Token is an API token record (the plaintext is never stored, only its hash).
// Kind is "opaque" for CLI keys or "jwt" for control tokens; for JWTs the stored
// hash is the token's jti.
type Token struct {
	ID         int
	Name       string
	Scopes     []string
	Kind       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	ExpiresAt  *time.Time
}

// User is a web-UI account. PasswordHash is an argon2id encoded hash.
type User struct {
	ID           int
	Username     string
	PasswordHash string
	Role         string // "admin" | "member"
	Disabled     bool
}

// SearchResult is one hit from a similarity or keyword search.
type SearchResult struct {
	FilePath  string
	Content   string
	Score     float64
	StartLine int
	EndLine   int
}

// IndexStore is the persistence subset needed to drive indexing and search:
// project/file/chunk lifecycle plus similarity and keyword lookup. It is
// deliberately smaller than Store so a standalone, PostgreSQL-free backend (see
// internal/localstore) can power the CLI's index/search path without
// implementing the server-only token, user, session and job operations.
type IndexStore interface {
	Close()
	Ping(ctx context.Context) error
	EnsureChunksTable(ctx context.Context, dims int) error
	UpsertProject(ctx context.Context, name, path, model string, dims int) (int, error)
	EnsureProjectIdentity(ctx context.Context, identity, name, path, model, sourceType string, dims int) (int, error)
	SetWorktreeFiles(ctx context.Context, projectID int, worktree string, files map[string]string) error
	PruneUnreferencedFiles(ctx context.Context, projectID int) (int64, error)
	CreateProject(ctx context.Context, name, model, sourceType, gitURL, branch string, dims int) (*Project, error)
	GetProject(ctx context.Context, name string) (*Project, error)
	GetProjectByID(ctx context.Context, id int) (*Project, error)
	GetProjectByIdentity(ctx context.Context, identity string) (*Project, error)
	ListProjects(ctx context.Context, limit, offset int) ([]Project, error)
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
	SearchSimilarWorktree(ctx context.Context, projectID int, embedding []float32, dims, topK int, worktree string) ([]SearchResult, error)
	SearchSimilarKeywordsWorktree(ctx context.Context, projectID int, queryText string, dims, topK int, worktree string) ([]SearchResult, error)
	DropAll(ctx context.Context) error
}

// Store is the full persistence surface the server depends on: the indexing and
// search subset (IndexStore) plus the server-only token, user, session and job
// operations.
type Store interface {
	IndexStore

	CreateToken(ctx context.Context, name, tokenHash string, scopes []string) (int, error)
	CreateUserToken(ctx context.Context, userID int, name, tokenHash string, scopes []string) (int, error)
	CreateJWTToken(ctx context.Context, userID int, name, jti string, scopes []string, expiresAt *time.Time) (int, error)
	TokenByHash(ctx context.Context, tokenHash string) (*Token, error)
	RevokeToken(ctx context.Context, id int) error
	RevokeUserToken(ctx context.Context, userID, id int) error
	ListUserTokens(ctx context.Context, userID int, kind string) ([]Token, error)
	CountTokens(ctx context.Context) (int, error)

	CreateUser(ctx context.Context, username, passwordHash, role string) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByID(ctx context.Context, id int) (*User, error)
	ListUsers(ctx context.Context, limit, offset int) ([]User, error)
	SetUserPassword(ctx context.Context, id int, passwordHash string) error
	SetUserDisabled(ctx context.Context, id int, disabled bool) error
	CountUsers(ctx context.Context) (int, error)

	CreateSession(ctx context.Context, tokenHash string, userID int, expiresAt time.Time) error
	SessionUser(ctx context.Context, tokenHash string) (*User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteExpiredSessions(ctx context.Context) (int64, error)

	EnqueueJob(ctx context.Context, projectID int, jobType string) (int, error)
	ClaimJob(ctx context.Context) (*Job, error)
	CompleteJob(ctx context.Context, id, filesIndexed, chunksCreated int) error
	FailJob(ctx context.Context, id int, errMsg string) error
	GetJob(ctx context.Context, id int) (*Job, error)
}

// PgStore is the PostgreSQL/pgvector implementation of Store.
type PgStore struct {
	pool *pgxpool.Pool
}

// compile-time assertion that PgStore satisfies Store.
var _ Store = (*PgStore)(nil)

// defaultPingDelays are the backoff intervals between initial-connection ping
// attempts (len+1 attempts total: 6 pings with 5 waits, 1s→16s).
var defaultPingDelays = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}

// pingWithBackoff pings the pool with exponential backoff so a slow-to-start
// Postgres does not turn into a restart loop.
func pingWithBackoff(ctx context.Context, pool *pgxpool.Pool) error {
	return retryPing(ctx, pool.Ping, defaultPingDelays)
}

// retryPing calls ping up to len(delays)+1 times, waiting delays[i] before the
// (i+1)-th retry. It returns the last error if every attempt fails, and honours
// context cancellation before and during each wait. It is split out from
// pingWithBackoff so the retry logic can be unit-tested without a live pool.
func retryPing(ctx context.Context, ping func(context.Context) error, delays []time.Duration) error {
	maxAttempts := len(delays) + 1
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err := ping(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == maxAttempts-1 {
			break
		}
		slog.Warn("db ping failed; retrying",
			"attempt", attempt+1, "max", maxAttempts, "err", lastErr, "retry_in", delays[attempt])

		// Respect context cancellation before and during the wait.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(delays[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

// NewPgStore connects (and pings) a pgxpool at connString and applies any
// pending schema migrations.
func NewPgStore(ctx context.Context, connString string) (*PgStore, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, err
	}
	if err := pingWithBackoff(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	if err := migrate(ctx, connString); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	s := &PgStore{pool: pool}
	if err := s.applyTrigramIndexes(ctx); err != nil {
		slog.Warn("trigram indexes (non-fatal)", "err", err)
	}
	return s, nil
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
	if _, err := s.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s USING hnsw %s", idxHNSW, table, opclass)); err != nil {
		return err
	}

	// GIN trigram index for ILIKE keyword search (see searchKeywords).
	trgmIdx := pgx.Identifier{fmt.Sprintf("chunks_%d_content_trgm", dims)}.Sanitize()
	_, err = s.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s USING gin (content gin_trgm_ops)", trgmIdx, table))
	return err
}

// applyTrigramIndexes scans for existing chunks_* tables (from prior runs) and
// adds GIN trigram indexes to each. Tables created after the migration runs
// via EnsureChunksTable already get the index; this catches tables that existed
// before the migration was applied.
func (s *PgStore) applyTrigramIndexes(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_name LIKE 'chunks_%'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tbl string
		if err := rows.Scan(&tbl); err != nil {
			continue
		}
		idx := pgx.Identifier{tbl + "_content_trgm"}.Sanitize()
		tblSafe := pgx.Identifier{tbl}.Sanitize()
		_, _ = s.pool.Exec(ctx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s USING gin (content gin_trgm_ops)", idx, tblSafe))
	}
	return rows.Err()
}

// distanceExpr is the cosine-distance SQL expression against query param $1,
// matching the HNSW index built for the given dimension.
func distanceExpr(dims int) string {
	if dims > hnswVectorLimit {
		return fmt.Sprintf("c.embedding::halfvec(%d) <=> $1::halfvec(%d)", dims, dims)
	}
	return "c.embedding <=> $1"
}

func (s *PgStore) UpsertProject(ctx context.Context, name, path, model string, dims int) (int, error) {
	var id int
	// name is no longer UNIQUE (F14), so upsert on identity instead — for this
	// legacy by-name API the identity is the name, keeping it idempotent per name.
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (name, path, model, status, identity, dims)
		VALUES ($1, $2, $3, 'indexing', $1, $4)
		ON CONFLICT (identity) DO UPDATE SET path = EXCLUDED.path, model = EXCLUDED.model, status = 'indexing', dims = EXCLUDED.dims
		RETURNING id
	`, name, path, model, dims).Scan(&id)
	return id, err
}

const projectColumns = `id, name, path, model, status, source_type, COALESCE(git_url, ''), COALESCE(branch, ''), COALESCE(identity, ''), COALESCE(dims, 0)`

func scanProject(row pgx.Row) (*Project, error) {
	var p Project
	if err := row.Scan(&p.ID, &p.Name, &p.Path, &p.Model, &p.Status, &p.SourceType, &p.GitURL, &p.Branch, &p.Identity, &p.Dims); err != nil {
		return nil, err
	}
	return &p, nil
}

// EnsureProjectIdentity upserts a project keyed by its stable identity, so any
// worktree or clone of the same repo maps to one project row. It returns the
// project id and sets status to "indexing".
func (s *PgStore) EnsureProjectIdentity(ctx context.Context, identity, name, path, model, sourceType string, dims int) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (name, path, model, status, source_type, identity, dims)
		VALUES ($1, $2, $3, 'indexing', $4, $5, $6)
		ON CONFLICT (identity) DO UPDATE
		  SET path = EXCLUDED.path, model = EXCLUDED.model, status = 'indexing', dims = EXCLUDED.dims
		RETURNING id
	`, name, path, model, sourceType, identity, dims).Scan(&id)
	return id, err
}

// SetWorktreeFiles replaces a worktree's manifest — the (path -> hash) set it
// currently has checked out — in one transaction.
func (s *PgStore) SetWorktreeFiles(ctx context.Context, projectID int, worktree string, files map[string]string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM worktree_files WHERE project_id = $1 AND worktree = $2`, projectID, worktree); err != nil {
		return err
	}
	if len(files) == 0 {
		return tx.Commit(ctx)
	}

	batch := &pgx.Batch{}
	for path, hash := range files {
		batch.Queue(`INSERT INTO worktree_files (project_id, worktree, path, hash) VALUES ($1, $2, $3, $4)`,
			projectID, worktree, path, hash)
	}
	br := tx.SendBatch(ctx, batch)
	defer func() { _ = br.Close() }()

	for range files {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PruneUnreferencedFiles deletes files (and, via ON DELETE CASCADE, their chunks)
// that no worktree of the project still references — bounding index growth to the
// union of all worktrees' current checkouts. Returns how many files were removed.
func (s *PgStore) PruneUnreferencedFiles(ctx context.Context, projectID int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM files f
		WHERE f.project_id = $1
		  AND NOT EXISTS (
		    SELECT 1 FROM worktree_files w
		    WHERE w.project_id = f.project_id AND w.path = f.path AND w.hash = f.hash
		  )
	`, projectID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *PgStore) GetProject(ctx context.Context, name string) (*Project, error) {
	p, err := scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE name = $1`, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// GetProjectByIdentity looks a project up by its stable identity (git identity
// or "path:<abs>"), which is unique — unlike the name, which is just a basename
// and can collide across folders.
func (s *PgStore) GetProjectByIdentity(ctx context.Context, identity string) (*Project, error) {
	p, err := scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE identity = $1`, identity))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// CreateProject registers a project with its content source. Returns
// ErrProjectExists if the name is taken.
func (s *PgStore) CreateProject(ctx context.Context, name, model, sourceType, gitURL, branch string, dims int) (*Project, error) {
	var id int
	// identity = name: server-registered projects are keyed by their (user-chosen,
	// unique) name via the identity unique index — the UNIQUE(name) constraint is
	// gone (F14, so index-path projects can share a basename), so name uniqueness
	// for API projects is preserved through identity instead.
	err := s.pool.QueryRow(ctx, `
		INSERT INTO projects (name, path, model, status, source_type, git_url, branch, identity, dims)
		VALUES ($1, '', $2, 'registered', $3, NULLIF($4, ''), NULLIF($5, ''), $1, $6)
		RETURNING id
	`, name, model, sourceType, gitURL, branch, dims).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation (identity)
			return nil, ErrProjectExists
		}
		return nil, err
	}
	return &Project{ID: id, Name: name, Model: model, Status: "registered", SourceType: sourceType, GitURL: gitURL, Branch: branch, Identity: name, Dims: dims}, nil
}

func (s *PgStore) ListProjects(ctx context.Context, limit, offset int) ([]Project, error) {
	var limitArg any
	if limit > 0 {
		limitArg = limit
	} // nil → LIMIT NULL (no limit)
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY name LIMIT $1 OFFSET $2`, limitArg, offset)
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
	// Content-addressed: (project, path, hash) is unique, so divergent versions of
	// the same path (across worktrees) coexist. Re-inserting an existing version
	// just refreshes its metadata.
	err := s.pool.QueryRow(ctx, `
		INSERT INTO files (project_id, path, hash, size_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, path, hash) DO UPDATE SET size_bytes = EXCLUDED.size_bytes, indexed_at = NOW()
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
	return s.searchSimilar(ctx, projectID, embedding, dims, topK, "")
}

// SearchSimilarWorktree is SearchSimilar restricted to the file versions a given
// worktree currently has checked out (via the worktree_files manifest), so each
// worktree sees its own content when the same path diverges across worktrees.
func (s *PgStore) SearchSimilarWorktree(ctx context.Context, projectID int, embedding []float32, dims, topK int, worktree string) ([]SearchResult, error) {
	return s.searchSimilar(ctx, projectID, embedding, dims, topK, worktree)
}

func (s *PgStore) searchSimilar(ctx context.Context, projectID int, embedding []float32, dims, topK int, worktree string) ([]SearchResult, error) {
	table, err := chunksTable(dims)
	if err != nil {
		return nil, err
	}
	// embedding IS NOT NULL excludes text-only rows (sensitive files stored
	// without an embedding); those surface via keyword search instead.
	dist := distanceExpr(dims)
	join := "JOIN files f ON f.id = c.file_id"
	args := []any{pgvector.NewVector(embedding), projectID, topK} // $1 vec, $2 project, $3 topK
	if worktree != "" {
		join += " JOIN worktree_files w ON w.project_id = c.project_id AND w.path = f.path AND w.hash = f.hash AND w.worktree = $4"
		args = append(args, worktree)
	}
	query := fmt.Sprintf(`
		SELECT f.path, c.content, c.start_line, c.end_line, 1 - (%s) AS score
		FROM %s c
		%s
		WHERE c.project_id = $2 AND c.embedding IS NOT NULL
		ORDER BY %s
		LIMIT $3
	`, dist, table, join, dist)

	rows, err := s.pool.Query(ctx, query, args...)
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

// RevokeUserToken revokes a token only if it belongs to userID (authorization is
// enforced in the query). Returns ErrNotFound when no matching active token exists.
func (s *PgStore) RevokeUserToken(ctx context.Context, userID, id int) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = NOW() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateJWTToken records an issued control token by its jti (kept in token_hash
// so the normal revocation/lookup path applies). expiresAt is nil for a
// non-expiring token.
func (s *PgStore) CreateJWTToken(ctx context.Context, userID int, name, jti string, scopes []string, expiresAt *time.Time) (int, error) {
	if scopes == nil {
		scopes = []string{}
	}
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (name, token_hash, scopes, user_id, kind, expires_at)
		VALUES ($1, $2, $3, $4, 'jwt', $5) RETURNING id
	`, name, jti, scopes, userID, expiresAt).Scan(&id)
	return id, err
}

// ListUserTokens returns a user's active (non-revoked) tokens of the given kind
// ("opaque" or "jwt"), newest first.
func (s *PgStore) ListUserTokens(ctx context.Context, userID int, kind string) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, scopes, kind, created_at, last_used_at, expires_at FROM api_tokens
		WHERE user_id = $1 AND kind = $2 AND revoked_at IS NULL ORDER BY created_at DESC
	`, userID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []Token
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Name, &t.Scopes, &t.Kind, &t.CreatedAt, &t.LastUsedAt, &t.ExpiresAt); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// CountTokens returns how many non-revoked tokens exist (used for bootstrap).
func (s *PgStore) CountTokens(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM api_tokens WHERE revoked_at IS NULL`).Scan(&n)
	return n, err
}

// CreateUserToken stores an API token owned by a user.
func (s *PgStore) CreateUserToken(ctx context.Context, userID int, name, tokenHash string, scopes []string) (int, error) {
	if scopes == nil {
		scopes = []string{}
	}
	var id int
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (name, token_hash, scopes, user_id) VALUES ($1, $2, $3, $4) RETURNING id
	`, name, tokenHash, scopes, userID).Scan(&id)
	return id, err
}

// CreateUser inserts a user, returning ErrUserExists on a duplicate username.
func (s *PgStore) CreateUser(ctx context.Context, username, passwordHash, role string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3)
		RETURNING id, username, password_hash, role, (disabled_at IS NOT NULL)
	`, username, passwordHash, role).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return nil, ErrUserExists
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *PgStore) scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByUsername returns a user by username (including disabled ones; callers
// decide whether disabled matters).
func (s *PgStore) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	return s.scanUser(s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, role, (disabled_at IS NOT NULL) FROM users WHERE username = $1
	`, username))
}

// GetUserByID returns a user by id.
func (s *PgStore) GetUserByID(ctx context.Context, id int) (*User, error) {
	return s.scanUser(s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash, role, (disabled_at IS NOT NULL) FROM users WHERE id = $1
	`, id))
}

// ListUsers returns all users ordered by username.
func (s *PgStore) ListUsers(ctx context.Context, limit, offset int) ([]User, error) {
	var limitArg any
	if limit > 0 {
		limitArg = limit
	} // nil → LIMIT NULL (no limit)
	rows, err := s.pool.Query(ctx, `
		SELECT id, username, password_hash, role, (disabled_at IS NOT NULL) FROM users ORDER BY username LIMIT $1 OFFSET $2
	`, limitArg, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// SetUserPassword updates a user's password hash.
func (s *PgStore) SetUserPassword(ctx context.Context, id int, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetUserDisabled disables or re-enables a user. Disabling also invalidates their
// active sessions.
func (s *PgStore) SetUserDisabled(ctx context.Context, id int, disabled bool) error {
	col := "NULL"
	if disabled {
		col = "NOW()"
	}
	tag, err := s.pool.Exec(ctx, `UPDATE users SET disabled_at = `+col+` WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if disabled {
		_, err = s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE user_id = $1`, id)
	}
	return err
}

// CountUsers returns the total number of users (used for admin bootstrap).
func (s *PgStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateSession stores a web session by its token hash.
func (s *PgStore) CreateSession(ctx context.Context, tokenHash string, userID int, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO web_sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)
	`, tokenHash, userID, expiresAt.UTC())
	return err
}

// SessionUser returns the user for a live (non-expired, non-disabled) session,
// or ErrNotFound.
func (s *PgStore) SessionUser(ctx context.Context, tokenHash string) (*User, error) {
	return s.scanUser(s.pool.QueryRow(ctx, `
		SELECT u.id, u.username, u.password_hash, u.role, (u.disabled_at IS NOT NULL)
		FROM web_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2 AND u.disabled_at IS NULL
	`, tokenHash, time.Now().UTC()))
}

// DeleteSession removes a session (logout); idempotent.
func (s *PgStore) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteExpiredSessions purges expired sessions and returns how many were removed.
func (s *PgStore) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM web_sessions WHERE expires_at <= $1`, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
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
	return s.searchKeywords(ctx, projectID, queryText, dims, topK, "")
}

// SearchSimilarKeywordsWorktree is the keyword search restricted to a worktree's
// checked-out file versions (see SearchSimilarWorktree).
func (s *PgStore) SearchSimilarKeywordsWorktree(ctx context.Context, projectID int, queryText string, dims, topK int, worktree string) ([]SearchResult, error) {
	return s.searchKeywords(ctx, projectID, queryText, dims, topK, worktree)
}

// resolveDims determines the embedding dimension for a project's chunks table,
// falling back to probing when the passed dims is non-positive.
func (s *PgStore) resolveDims(ctx context.Context, projectID, dims int) int {
	if dims <= 0 {
		if p, err := s.GetProjectByID(ctx, projectID); err == nil && p.Dims > 0 {
			return p.Dims
		}
	}
	if dims <= 0 {
		if d := s.probeDimsForProject(ctx, projectID); d > 0 {
			return d
		}
	}
	if dims <= 0 {
		return 1024
	}
	return dims
}

func (s *PgStore) searchKeywords(ctx context.Context, projectID int, queryText string, dims, topK int, worktree string) ([]SearchResult, error) {
	dims = s.resolveDims(ctx, projectID, dims)

	table, err := chunksTable(dims)
	if err != nil {
		return nil, err
	}

	words := filterSearchWords(queryText)
	if len(words) == 0 {
		return nil, nil
	}

	clauses := make([]string, 0, len(words))
	args := []any{projectID}
	for i, w := range words {
		clauses = append(clauses, fmt.Sprintf("c.content ILIKE $%d", i+2))
		args = append(args, "%"+w+"%")
	}

	join := "JOIN files f ON f.id = c.file_id"
	topKIdx := len(args) + 1
	args = append(args, topK)
	if worktree != "" {
		args = append(args, worktree)
		join += fmt.Sprintf(" JOIN worktree_files w ON w.project_id = c.project_id AND w.path = f.path AND w.hash = f.hash AND w.worktree = $%d", len(args))
	}

	sql := fmt.Sprintf(`
		SELECT f.path, c.content, c.start_line, c.end_line, 0.5 AS score
		FROM %s c
		%s
		WHERE c.project_id = $1 AND (%s)
		LIMIT $%d
	`, table, join, strings.Join(clauses, " OR "), topKIdx)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSearchRows(rows)
}

// filterSearchWords filters and normalises query words for keyword search:
// removes terms shorter than 3 characters and caps at 20 terms to prevent
// wasteful scans and DoS via query explosion. Returns nil if no valid words remain.
func filterSearchWords(queryText string) []string {
	words := strings.Fields(queryText)
	if len(words) == 0 {
		return nil
	}
	filtered := words[:0]
	for _, w := range words {
		if len(w) >= 3 {
			filtered = append(filtered, w)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) > 20 {
		filtered = filtered[:20]
	}
	return filtered
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

	if len(tables) == 0 {
		return 0
	}

	var parts []string
	for _, tbl := range tables {
		sanitized := pgx.Identifier{tbl}.Sanitize()
		parts = append(parts, fmt.Sprintf("SELECT '%s'::text AS tbl FROM %s WHERE project_id = $1", tbl, sanitized))
	}
	q := strings.Join(parts, " UNION ALL ") + " LIMIT 1"

	var found string
	if err := s.pool.QueryRow(ctx, q, projectID).Scan(&found); err != nil {
		return 0
	}
	var d int
	if _, err := fmt.Sscanf(found, "chunks_%d", &d); err == nil && d > 0 {
		return d
	}
	return 0
}
