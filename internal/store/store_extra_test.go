package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestNewPgStoreConnectError needs no container: connecting to a dead address
// makes the initial Ping fail, exercising NewPgStore's error path.
func TestNewPgStoreConnectError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := NewPgStore(ctx, "postgres://nobody:nobody@127.0.0.1:1/nope?sslmode=disable"); err == nil {
		t.Error("NewPgStore against a dead address should error")
	}
}

func TestDistanceExprBranches(t *testing.T) {
	if got := distanceExpr(3); got != "c.embedding <=> $1" {
		t.Errorf("distanceExpr(3) = %q, want plain vector expr", got)
	}
	got := distanceExpr(3072)
	if got == "c.embedding <=> $1" {
		t.Errorf("distanceExpr(3072) should use the halfvec cast, got %q", got)
	}
}

// TestPingAndFailJob bundles several small gaps into one container to keep the
// (slow) container count down.
func TestPingAndFailJob(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	// FailJob path: enqueue → claim → fail → observe failed status + error text.
	p, _ := s.CreateProject(ctx, "proj", "bge-m3", "push", "", "")
	id, err := s.EnqueueJob(ctx, p.ID, "full")
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := s.ClaimJob(ctx); err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if err := s.FailJob(ctx, id, "embedding provider unreachable"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}
	job, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != "failed" || job.Error != "embedding provider unreachable" {
		t.Errorf("job after FailJob = %+v, want failed with error text", job)
	}
}

// TestUserAdminEdgeCases covers the not-found and re-enable branches of the
// user-mutation methods.
func TestUserAdminEdgeCases(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Mutations on an unknown id map to ErrNotFound.
	if err := s.SetUserPassword(ctx, 99999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetUserPassword(unknown) = %v, want ErrNotFound", err)
	}
	if err := s.SetUserDisabled(ctx, 99999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetUserDisabled(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := s.GetUserByID(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserByID(unknown) = %v, want ErrNotFound", err)
	}

	// Re-enabling a user (disabled=false) takes the non-session-deleting branch.
	u, err := s.CreateUser(ctx, "erin", "hash", "member")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := s.SetUserDisabled(ctx, u.ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := s.SetUserDisabled(ctx, u.ID, false); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	got, err := s.GetUserByID(ctx, u.ID)
	if err != nil || got.Disabled {
		t.Errorf("after re-enable = %+v, err %v; want enabled", got, err)
	}
}

// TestHighDimHalfvec covers the >2000-dim (halfvec) branches of
// EnsureChunksTable, distanceExpr and SearchSimilar against a real pgvector.
func TestHighDimHalfvec(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const dims = 3072
	if err := s.EnsureChunksTable(ctx, dims); err != nil {
		t.Fatalf("EnsureChunksTable(%d): %v", dims, err)
	}
	pid, _ := s.UpsertProject(ctx, "hd", "/hd", "gemini-embedding-2")
	fid, _ := s.UpsertFile(ctx, pid, "doc.txt", "h", 10)

	vec := make([]float32, dims)
	vec[0] = 1
	if err := s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "high-dimensional chunk", StartLine: 1, EndLine: 1}},
		[][]float32{vec}, dims); err != nil {
		t.Fatalf("InsertChunks(highdim): %v", err)
	}

	res, err := s.SearchSimilar(ctx, pid, vec, dims, 5)
	if err != nil {
		t.Fatalf("SearchSimilar(highdim): %v", err)
	}
	if len(res) != 1 || res[0].Content != "high-dimensional chunk" {
		t.Fatalf("halfvec search = %+v, want the high-dim chunk", res)
	}
}

func TestChunksTableBoundaries(t *testing.T) {
	// Valid extremes stay valid; out-of-range values are rejected.
	if _, err := chunksTable(1); err != nil {
		t.Errorf("chunksTable(1) should be valid: %v", err)
	}
	if _, err := chunksTable(maxDims); err != nil {
		t.Errorf("chunksTable(maxDims) should be valid: %v", err)
	}
	for _, bad := range []int{0, -1, maxDims + 1} {
		if _, err := chunksTable(bad); err == nil {
			t.Errorf("chunksTable(%d) should be rejected", bad)
		}
	}
}

// TestStoreErrorAndBoundaryPaths bundles adversarial coverage into one container:
// exact topK/ordering assertions, invalid-dimension rejection on every
// dims-taking method, the chunks/embeddings length guard, and a sweep of every
// query method under a cancelled context to exercise their error-return paths.
func TestStoreErrorAndBoundaryPaths(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, _ := s.UpsertProject(ctx, "p", "/p", "test-3d")
	fid, _ := s.UpsertFile(ctx, pid, "f.go", "h", 1)

	// Insert a known corpus so ranking and topK can be asserted exactly.
	chunks := []chunker.Chunk{
		{Content: "aligned", StartLine: 1, EndLine: 1},
		{Content: "orthogonal", StartLine: 2, EndLine: 2},
		{Content: "opposite", StartLine: 3, EndLine: 3},
	}
	embs := [][]float32{{1, 0, 0}, {0, 1, 0}, {-1, 0, 0}}
	if err := s.InsertChunks(ctx, pid, fid, chunks, embs, 3); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	// Exact ranking: query [1,0,0] ranks "aligned" first, then "orthogonal", and
	// topK caps the count precisely.
	res, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 2)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("topK=2 returned %d results, want exactly 2", len(res))
	}
	if res[0].Content != "aligned" {
		t.Errorf("top hit = %q, want aligned", res[0].Content)
	}
	if res[0].Score <= res[1].Score {
		t.Errorf("scores not strictly descending: %v <= %v", res[0].Score, res[1].Score)
	}
	if res[0].StartLine != 1 || res[0].EndLine != 1 {
		t.Errorf("line range = [%d,%d], want [1,1]", res[0].StartLine, res[0].EndLine)
	}

	// The chunks/embeddings length guard.
	if err := s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "a"}, {Content: "b"}}, [][]float32{{1, 0, 0}}, 3); err == nil {
		t.Error("mismatched chunks/embeddings lengths should error")
	}

	// Every dims-taking method rejects an out-of-range dimension.
	badDims := maxDims + 1
	if err := s.EnsureChunksTable(ctx, badDims); err == nil {
		t.Error("EnsureChunksTable(bad dims) should error")
	}
	if err := s.DeleteChunksForFile(ctx, pid, fid, badDims); err == nil {
		t.Error("DeleteChunksForFile(bad dims) should error")
	}
	if err := s.InsertChunks(ctx, pid, fid, chunks[:1], embs[:1], badDims); err == nil {
		t.Error("InsertChunks(bad dims) should error")
	}
	if err := s.InsertChunksTextOnly(ctx, pid, fid, chunks[:1], badDims); err == nil {
		t.Error("InsertChunksTextOnly(bad dims) should error")
	}
	if _, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, badDims, 5); err == nil {
		t.Error("SearchSimilar(bad dims) should error")
	}
	if _, err := s.SearchSimilarKeywords(ctx, pid, "x", badDims, 5); err == nil {
		t.Error("SearchSimilarKeywords(bad dims) should error")
	}
	if _, err := s.FileUpToDate(ctx, pid, "f.go", "h", badDims); err == nil {
		t.Error("FileUpToDate(bad dims) should error")
	}

	// Empty keyword query short-circuits to no results, no error.
	if r, err := s.SearchSimilarKeywords(ctx, pid, "   ", 3, 5); err != nil || r != nil {
		t.Errorf("empty keyword query = %v, err %v; want nil,nil", r, err)
	}

	// --- cancelled-context sweep: every query method must surface an error ---
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	errExp := func(name string, err error) {
		if err == nil {
			t.Errorf("%s under cancelled ctx = nil, want error", name)
		}
	}

	errExp("Ping", s.Ping(cctx))
	_, e := s.UpsertProject(cctx, "x", "/x", "m")
	errExp("UpsertProject", e)
	_, e = s.CreateProject(cctx, "x", "m", "path", "", "")
	errExp("CreateProject", e)
	_, e = s.GetProject(cctx, "p")
	errExp("GetProject", e)
	_, e = s.GetProjectByID(cctx, pid)
	errExp("GetProjectByID", e)
	_, e = s.ListProjects(cctx)
	errExp("ListProjects", e)
	errExp("DeleteProject", s.DeleteProject(cctx, "p"))
	errExp("UpdateProjectStatus", s.UpdateProjectStatus(cctx, pid, "ready"))
	_, e = s.UpsertFile(cctx, pid, "f.go", "h", 1)
	errExp("UpsertFile", e)
	_, e = s.FileUpToDate(cctx, pid, "f.go", "h", 3)
	errExp("FileUpToDate", e)
	_, e = s.ListFileHashes(cctx, pid)
	errExp("ListFileHashes", e)
	errExp("DeleteFileByPath", s.DeleteFileByPath(cctx, pid, "f.go"))
	errExp("DeleteChunksForFile", s.DeleteChunksForFile(cctx, pid, fid, 3))
	errExp("InsertChunks", s.InsertChunks(cctx, pid, fid, chunks[:1], embs[:1], 3))
	errExp("InsertChunksTextOnly", s.InsertChunksTextOnly(cctx, pid, fid, chunks[:1], 3))
	_, e = s.SearchSimilar(cctx, pid, []float32{1, 0, 0}, 3, 5)
	errExp("SearchSimilar", e)
	_, e = s.SearchSimilarKeywords(cctx, pid, "aligned", 3, 5)
	errExp("SearchSimilarKeywords", e)
	// dims=0 forces the probeDimsForProject path under a cancelled context too.
	_, e = s.SearchSimilarKeywords(cctx, pid, "aligned", 0, 5)
	errExp("SearchSimilarKeywords(probe)", e)
	errExp("DropAll", s.DropAll(cctx))

	_, e = s.CreateToken(cctx, "n", "h", []string{"read"})
	errExp("CreateToken", e)
	_, e = s.TokenByHash(cctx, "h")
	errExp("TokenByHash", e)
	errExp("RevokeToken", s.RevokeToken(cctx, 1))
	errExp("RevokeUserToken", s.RevokeUserToken(cctx, 1, 1))
	_, e = s.CreateJWTToken(cctx, 1, "n", "jti", []string{"read"}, nil)
	errExp("CreateJWTToken", e)
	_, e = s.ListUserTokens(cctx, 1, "opaque")
	errExp("ListUserTokens", e)
	_, e = s.CountTokens(cctx)
	errExp("CountTokens", e)
	_, e = s.CreateUserToken(cctx, 1, "n", "h", []string{"read"})
	errExp("CreateUserToken", e)

	_, e = s.CreateUser(cctx, "u", "h", "member")
	errExp("CreateUser", e)
	_, e = s.GetUserByUsername(cctx, "u")
	errExp("GetUserByUsername", e)
	_, e = s.GetUserByID(cctx, 1)
	errExp("GetUserByID", e)
	_, e = s.ListUsers(cctx)
	errExp("ListUsers", e)
	errExp("SetUserPassword", s.SetUserPassword(cctx, 1, "h"))
	errExp("SetUserDisabled", s.SetUserDisabled(cctx, 1, true))
	_, e = s.CountUsers(cctx)
	errExp("CountUsers", e)

	errExp("CreateSession", s.CreateSession(cctx, "h", 1, time.Now().Add(time.Hour)))
	_, e = s.SessionUser(cctx, "h")
	errExp("SessionUser", e)
	errExp("DeleteSession", s.DeleteSession(cctx, "h"))
	_, e = s.DeleteExpiredSessions(cctx)
	errExp("DeleteExpiredSessions", e)

	_, e = s.EnqueueJob(cctx, pid, "full")
	errExp("EnqueueJob", e)
	_, e = s.ClaimJob(cctx)
	errExp("ClaimJob", e)
	errExp("CompleteJob", s.CompleteJob(cctx, 1, 1, 1))
	errExp("FailJob", s.FailJob(cctx, 1, "x"))
	_, e = s.GetJob(cctx, 1)
	errExp("GetJob", e)
}
