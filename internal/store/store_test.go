package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestChunksTableValidation is a pure unit test (no container) for the dynamic
// table-name guard.
func TestChunksTableValidation(t *testing.T) {
	for _, bad := range []int{0, -1, maxDims + 1} {
		if _, err := chunksTable(bad); err == nil {
			t.Errorf("chunksTable(%d) = nil error, want rejection", bad)
		}
	}
	got, err := chunksTable(1024)
	if err != nil {
		t.Fatalf("chunksTable(1024) errored: %v", err)
	}
	if got != `"chunks_1024"` {
		t.Errorf("chunksTable(1024) = %s, want quoted identifier \"chunks_1024\"", got)
	}
}

// newTestStore starts a throwaway pgvector container and applies the base
// schema (mirrors init.sql). Skips when no Docker provider is available.
func newTestStore(t *testing.T) *PgStore {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "pgvector/pgvector:pg16",
		postgres.WithDatabase("semantic_indexer"),
		postgres.WithUsername("semantic"),
		postgres.WithPassword("semantic"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(180*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start pgvector container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// NewPgStore applies the goose migrations, so the schema is ready here.
	s, err := NewPgStore(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPgStore: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestProjectLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.UpsertProject(ctx, "demo", "/tmp/demo", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	p, err := s.GetProject(ctx, "demo")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if p.ID != id || p.Path != "/tmp/demo" || p.Model != "bge-m3" || p.Status != "indexing" {
		t.Errorf("GetProject = %+v, unexpected", p)
	}

	if err := s.UpdateProjectStatus(ctx, id, "ready"); err != nil {
		t.Fatalf("UpdateProjectStatus: %v", err)
	}
	p, _ = s.GetProject(ctx, "demo")
	if p.Status != "ready" {
		t.Errorf("status = %q, want ready", p.Status)
	}

	// Upsert is idempotent on name and resets status to indexing.
	id2, _ := s.UpsertProject(ctx, "demo", "/tmp/demo2", "bge-m3", 0)
	if id2 != id {
		t.Errorf("re-upsert changed id: %d != %d", id2, id)
	}
}

func TestChunkRoundTripAndSearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, err := s.UpsertProject(ctx, "proj", "/tmp/proj", "test-3d", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	fid, err := s.UpsertFile(ctx, pid, "src/auth.go", "hash1", 100)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	chunks := []chunker.Chunk{
		{Content: "alpha auth token handler", StartLine: 10, EndLine: 12},
		{Content: "beta gamma delta", StartLine: 20, EndLine: 20},
	}
	embeddings := [][]float32{{1, 0, 0}, {0, 1, 0}}
	if err := s.InsertChunks(ctx, pid, fid, chunks, embeddings, 3); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	// Vector search: query closest to the first chunk's embedding.
	res, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 5)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("SearchSimilar returned %d results, want 2", len(res))
	}
	if res[0].Content != "alpha auth token handler" {
		t.Errorf("top hit = %q, want the alpha chunk", res[0].Content)
	}
	if res[0].Score <= res[1].Score {
		t.Errorf("scores not descending: %v <= %v", res[0].Score, res[1].Score)
	}
	if res[0].FilePath != "src/auth.go" {
		t.Errorf("file path = %q, want src/auth.go", res[0].FilePath)
	}
	if res[0].StartLine != 10 || res[0].EndLine != 12 {
		t.Errorf("top hit line range = [%d,%d], want [10,12]", res[0].StartLine, res[0].EndLine)
	}

	// Keyword fallback: ILIKE on content.
	kw, err := s.SearchSimilarKeywords(ctx, pid, "auth", 3, 5)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords: %v", err)
	}
	if len(kw) != 1 || kw[0].Content != "alpha auth token handler" {
		t.Errorf("keyword search = %+v, want the alpha chunk", kw)
	}

	// Keyword search with unknown dims should probe and still find the chunk.
	kw2, err := s.SearchSimilarKeywords(ctx, pid, "gamma", 0, 5)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords(dims=0): %v", err)
	}
	if len(kw2) != 1 || kw2[0].Content != "beta gamma delta" {
		t.Errorf("probed keyword search = %+v, want the beta chunk", kw2)
	}

	// Re-inserting the same chunk indexes (upsert) rather than duplicating.
	if err := s.InsertChunks(ctx, pid, fid, chunks[:1], embeddings[:1], 3); err != nil {
		t.Fatalf("re-InsertChunks: %v", err)
	}
	again, _ := s.SearchSimilarKeywords(ctx, pid, "auth", 3, 5)
	if len(again) != 1 {
		t.Errorf("after re-insert got %d rows, want 1 (upsert, no dup)", len(again))
	}

	// DropAll clears everything.
	if err := s.DropAll(ctx); err != nil {
		t.Fatalf("DropAll: %v", err)
	}
	if _, err := s.GetProject(ctx, "proj"); err == nil {
		t.Error("GetProject after DropAll should fail (no rows)")
	}
}

func TestTextOnlyChunksKeywordOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, _ := s.UpsertProject(ctx, "p", "/p", "test-3d", 0)

	// One embedded chunk and one text-only (embedding NULL) chunk.
	efid, _ := s.UpsertFile(ctx, pid, "code.go", "h1", 1)
	_ = s.InsertChunks(ctx, pid, efid, []chunker.Chunk{{Content: "embedded vector chunk"}}, [][]float32{{1, 0, 0}}, 3)

	tfid, _ := s.UpsertFile(ctx, pid, ".env", "h2", 1)
	if err := s.InsertChunksTextOnly(ctx, pid, tfid, []chunker.Chunk{{Content: "SECRET_TOKEN plaintext only"}}, 3); err != nil {
		t.Fatalf("InsertChunksTextOnly: %v", err)
	}

	// Vector search must NOT return the text-only (NULL embedding) row.
	vec, err := s.SearchSimilar(ctx, pid, []float32{0, 0, 1}, 3, 10)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	for _, r := range vec {
		if r.FilePath == ".env" {
			t.Error("SECURITY: text-only chunk leaked into vector search results")
		}
	}

	// Keyword search MUST find the text-only content.
	kw, err := s.SearchSimilarKeywords(ctx, pid, "SECRET_TOKEN", 3, 10)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords: %v", err)
	}
	if len(kw) != 1 || kw[0].FilePath != ".env" {
		t.Errorf("keyword search = %+v, want the text-only .env chunk", kw)
	}
}

func TestFileUpToDate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, _ := s.UpsertProject(ctx, "p", "/p", "test-3d", 0)
	fid, _ := s.UpsertFile(ctx, pid, "x.go", "hash-v1", 10)

	// No chunks yet → not up to date even though the hash matches.
	if up, err := s.FileUpToDate(ctx, pid, "x.go", "hash-v1", 3); err != nil || up {
		t.Errorf("FileUpToDate (no chunks) = %v, err %v; want false", up, err)
	}

	_ = s.InsertChunks(ctx, pid, fid, []chunker.Chunk{{Content: "code"}}, [][]float32{{1, 0, 0}}, 3)

	// Same hash + chunks present → up to date.
	if up, err := s.FileUpToDate(ctx, pid, "x.go", "hash-v1", 3); err != nil || !up {
		t.Errorf("FileUpToDate (hash match + chunks) = %v, err %v; want true", up, err)
	}
	// Changed hash → not up to date (needs reindex).
	if up, err := s.FileUpToDate(ctx, pid, "x.go", "hash-v2", 3); err != nil || up {
		t.Errorf("FileUpToDate (hash changed) = %v, err %v; want false", up, err)
	}
	// Unknown file → not up to date.
	if up, err := s.FileUpToDate(ctx, pid, "other.go", "hash-v1", 3); err != nil || up {
		t.Errorf("FileUpToDate (unknown file) = %v, err %v; want false", up, err)
	}
}

func TestProjectCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, err := s.CreateProject(ctx, "repo", "bge-m3", "git", "https://x/y.git", "main", 0)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.SourceType != "git" || p.GitURL != "https://x/y.git" || p.Status != "registered" {
		t.Errorf("created project = %+v", p)
	}

	// Duplicate name → ErrProjectExists.
	if _, err := s.CreateProject(ctx, "repo", "m", "push", "", "", 0); !errors.Is(err, ErrProjectExists) {
		t.Errorf("duplicate CreateProject err = %v, want ErrProjectExists", err)
	}

	// GetProject returns the source fields.
	got, err := s.GetProject(ctx, "repo")
	if err != nil || got.GitURL != "https://x/y.git" || got.Branch != "main" {
		t.Errorf("GetProject = %+v, err %v", got, err)
	}
	// Unknown → ErrNotFound.
	if _, err := s.GetProject(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProject(ghost) err = %v, want ErrNotFound", err)
	}

	// List includes it.
	list, err := s.ListProjects(ctx, 0, 0)
	if err != nil || len(list) != 1 || list[0].Name != "repo" {
		t.Errorf("ListProjects = %+v, err %v", list, err)
	}

	// Delete, then it's gone; deleting again → ErrNotFound.
	if err := s.DeleteProject(ctx, "repo"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if err := s.DeleteProject(ctx, "repo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteProject err = %v, want ErrNotFound", err)
	}
}

func TestIndexJobs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, _ := s.CreateProject(ctx, "proj", "bge-m3", "git", "https://x/y.git", "", 0)

	// GetProjectByID round-trips.
	if got, err := s.GetProjectByID(ctx, p.ID); err != nil || got.Name != "proj" {
		t.Fatalf("GetProjectByID = %+v, err %v", got, err)
	}

	id, err := s.EnqueueJob(ctx, p.ID, "full")
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	claimed, err := s.ClaimJob(ctx)
	if err != nil || claimed == nil || claimed.ID != id || claimed.Status != "running" {
		t.Fatalf("ClaimJob = %+v, err %v; want the queued job running", claimed, err)
	}
	// Nothing else queued.
	if again, _ := s.ClaimJob(ctx); again != nil {
		t.Errorf("second ClaimJob = %+v, want nil", again)
	}

	if err := s.CompleteJob(ctx, id, 7, 42); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	job, _ := s.GetJob(ctx, id)
	if job.Status != "succeeded" || job.FilesIndexed != 7 || job.ChunksCreated != 42 {
		t.Errorf("job after complete = %+v", job)
	}
	if _, err := s.GetJob(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetJob(unknown) err = %v, want ErrNotFound", err)
	}
}

// SKIP LOCKED guarantee: concurrent workers claim each job exactly once.
func TestClaimJobConcurrentNoDoubleClaim(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, _ := s.CreateProject(ctx, "proj", "bge-m3", "push", "", "", 0)

	const n = 20
	for i := 0; i < n; i++ {
		if _, err := s.EnqueueJob(ctx, p.ID, "full"); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := map[int]int{}
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := s.ClaimJob(ctx)
				if err != nil || job == nil {
					return
				}
				mu.Lock()
				seen[job.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Errorf("claimed %d distinct jobs, want %d", len(seen), n)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("job %d claimed %d times (want exactly 1)", id, count)
		}
	}
}

func TestAPITokens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if n, err := s.CountTokens(ctx); err != nil || n != 0 {
		t.Fatalf("CountTokens on empty = %d, err %v; want 0", n, err)
	}

	id, err := s.CreateToken(ctx, "ci", "hash-abc", []string{"read", "write"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	tok, err := s.TokenByHash(ctx, "hash-abc")
	if err != nil || tok == nil {
		t.Fatalf("TokenByHash = %v, err %v; want a token", tok, err)
	}
	if tok.ID != id || tok.Name != "ci" || len(tok.Scopes) != 2 {
		t.Errorf("token = %+v, unexpected", tok)
	}

	// Unknown hash → (nil, nil).
	if tok, err := s.TokenByHash(ctx, "nope"); err != nil || tok != nil {
		t.Errorf("TokenByHash(unknown) = %v, err %v; want nil", tok, err)
	}

	if n, _ := s.CountTokens(ctx); n != 1 {
		t.Errorf("CountTokens = %d, want 1", n)
	}

	// Revoked tokens are no longer returned and don't count.
	if err := s.RevokeToken(ctx, id); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if tok, err := s.TokenByHash(ctx, "hash-abc"); err != nil || tok != nil {
		t.Errorf("TokenByHash(revoked) = %v, err %v; want nil", tok, err)
	}
	if n, _ := s.CountTokens(ctx); n != 0 {
		t.Errorf("CountTokens after revoke = %d, want 0", n)
	}
}

func TestUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if n, err := s.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers on empty = %d, err %v; want 0", n, err)
	}

	admin, err := s.CreateUser(ctx, "admin", "hash1", "admin")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if admin.ID == 0 || admin.Role != "admin" || admin.Disabled {
		t.Errorf("admin = %+v, unexpected", admin)
	}

	// Duplicate username → ErrUserExists.
	if _, err := s.CreateUser(ctx, "admin", "hash2", "member"); !errors.Is(err, ErrUserExists) {
		t.Errorf("duplicate CreateUser err = %v; want ErrUserExists", err)
	}

	got, err := s.GetUserByUsername(ctx, "admin")
	if err != nil || got.ID != admin.ID || got.PasswordHash != "hash1" {
		t.Errorf("GetUserByUsername = %+v, err %v", got, err)
	}
	if _, err := s.GetUserByUsername(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserByUsername(ghost) err = %v; want ErrNotFound", err)
	}

	if err := s.SetUserPassword(ctx, admin.ID, "hash-new"); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	if got, _ := s.GetUserByID(ctx, admin.ID); got.PasswordHash != "hash-new" {
		t.Errorf("password not updated: %q", got.PasswordHash)
	}

	if _, err := s.CreateUser(ctx, "bob", "h", "member"); err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}
	users, err := s.ListUsers(ctx, 0, 0)
	if err != nil || len(users) != 2 {
		t.Fatalf("ListUsers = %d users, err %v; want 2", len(users), err)
	}
	if n, _ := s.CountUsers(ctx); n != 2 {
		t.Errorf("CountUsers = %d, want 2", n)
	}
}

func TestSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "alice", "hash", "member")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateSession(ctx, "sess-hash", u.ID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := s.SessionUser(ctx, "sess-hash")
	if err != nil || got.ID != u.ID {
		t.Fatalf("SessionUser = %+v, err %v", got, err)
	}

	// Expired session → ErrNotFound.
	if err := s.CreateSession(ctx, "old", u.ID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SessionUser(expired) err = %v; want ErrNotFound", err)
	}
	if n, err := s.DeleteExpiredSessions(ctx); err != nil || n != 1 {
		t.Errorf("DeleteExpiredSessions = %d, err %v; want 1", n, err)
	}

	// Disabling the user drops their sessions and blocks lookup.
	if err := s.SetUserDisabled(ctx, u.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	if _, err := s.SessionUser(ctx, "sess-hash"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SessionUser after disable err = %v; want ErrNotFound", err)
	}

	// Logout is idempotent.
	if err := s.DeleteSession(ctx, "sess-hash"); err != nil {
		t.Errorf("DeleteSession: %v", err)
	}
}

func TestCreateUserTokenLinksOwner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "carol", "hash", "member")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateUserToken(ctx, u.ID, "laptop", "tok-hash", []string{"read"})
	if err != nil || id == 0 {
		t.Fatalf("CreateUserToken = %d, err %v", id, err)
	}
	tok, err := s.TokenByHash(ctx, "tok-hash")
	if err != nil || tok == nil || tok.Name != "laptop" {
		t.Errorf("TokenByHash = %+v, err %v", tok, err)
	}

	owned, err := s.ListUserTokens(ctx, u.ID, "opaque")
	if err != nil || len(owned) != 1 || owned[0].Name != "laptop" || owned[0].CreatedAt.IsZero() {
		t.Fatalf("ListUserTokens = %+v, err %v", owned, err)
	}
	if owned[0].Kind != "opaque" {
		t.Errorf("opaque token Kind = %q", owned[0].Kind)
	}

	// A different user cannot revoke this token.
	other, _ := s.CreateUser(ctx, "dave", "h", "member")
	if err := s.RevokeUserToken(ctx, other.ID, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user revoke err = %v; want ErrNotFound", err)
	}
	// The owner can, after which it disappears from the list.
	if err := s.RevokeUserToken(ctx, u.ID, id); err != nil {
		t.Fatalf("RevokeUserToken(owner): %v", err)
	}
	if owned, _ := s.ListUserTokens(ctx, u.ID, "opaque"); len(owned) != 0 {
		t.Errorf("ListUserTokens after revoke = %d; want 0", len(owned))
	}
}

func TestJWTTokenRecord(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "svc", "h", "member")
	if err != nil {
		t.Fatal(err)
	}

	// A non-expiring JWT record (expiresAt nil).
	if _, err := s.CreateJWTToken(ctx, u.ID, "deploy", "jti-1", []string{"write"}, nil); err != nil {
		t.Fatalf("CreateJWTToken: %v", err)
	}
	// JWTs are listed under kind "jwt", not "opaque".
	if opaque, _ := s.ListUserTokens(ctx, u.ID, "opaque"); len(opaque) != 0 {
		t.Errorf("JWT leaked into opaque list: %d", len(opaque))
	}
	jwts, err := s.ListUserTokens(ctx, u.ID, "jwt")
	if err != nil || len(jwts) != 1 || jwts[0].Kind != "jwt" || jwts[0].ExpiresAt != nil {
		t.Fatalf("ListUserTokens(jwt) = %+v, err %v", jwts, err)
	}
	// The jti is looked up like any token hash (this is the revocation check).
	if tok, _ := s.TokenByHash(ctx, "jti-1"); tok == nil {
		t.Error("TokenByHash(jti) = nil; want the active JWT record")
	}
}

func TestListFileHashesAndDeleteByPath(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, _ := s.UpsertProject(ctx, "p", "/p", "test-3d", 0)
	fidA, _ := s.UpsertFile(ctx, pid, "a.go", "h1", 10)
	_, _ = s.UpsertFile(ctx, pid, "b.go", "h2", 20)
	_ = s.InsertChunks(ctx, pid, fidA, []chunker.Chunk{{Content: "alpha"}}, [][]float32{{1, 0, 0}}, 3)

	hashes, err := s.ListFileHashes(ctx, pid)
	if err != nil || hashes["a.go"] != "h1" || hashes["b.go"] != "h2" || len(hashes) != 2 {
		t.Fatalf("ListFileHashes = %v, err %v", hashes, err)
	}

	// Deleting a.go removes it and cascades its chunks.
	if err := s.DeleteFileByPath(ctx, pid, "a.go"); err != nil {
		t.Fatalf("DeleteFileByPath: %v", err)
	}
	hashes, _ = s.ListFileHashes(ctx, pid)
	if _, ok := hashes["a.go"]; ok || len(hashes) != 1 {
		t.Errorf("after delete hashes = %v, want only b.go", hashes)
	}
	if res, _ := s.SearchSimilarKeywords(ctx, pid, "alpha", 3, 5); len(res) != 0 {
		t.Errorf("chunks of deleted file not cascaded: %v", res)
	}
}

func TestDeleteChunksForFile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, _ := s.UpsertProject(ctx, "p", "/p", "test-3d", 0)
	fid, _ := s.UpsertFile(ctx, pid, "f.go", "h", 1)
	_ = s.InsertChunks(ctx, pid, fid, []chunker.Chunk{{Content: "keepme"}}, [][]float32{{1, 0, 0}}, 3)

	if err := s.DeleteChunksForFile(ctx, pid, fid, 3); err != nil {
		t.Fatalf("DeleteChunksForFile: %v", err)
	}
	res, _ := s.SearchSimilarKeywords(ctx, pid, "keepme", 3, 5)
	if len(res) != 0 {
		t.Errorf("after delete got %d rows, want 0", len(res))
	}
}
