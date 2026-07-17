package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestEnsureProjectIdentityAndIdentityLookup ensures EnsureProjectIdentity
// upserts by stable identity and GetProjectByIdentity resolves it, including
// the ErrNotFound path.
func TestEnsureProjectIdentityAndIdentityLookup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.EnsureProjectIdentity(ctx, "git:r1", "myrepo", "/path/to/myrepo", "bge-m3", "git", 0)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity: %v", err)
	}

	// Get by identity.
	p, err := s.GetProjectByIdentity(ctx, "git:r1")
	if err != nil {
		t.Fatalf("GetProjectByIdentity: %v", err)
	}
	if p.ID != id || p.Name != "myrepo" || p.SourceType != "git" {
		t.Errorf("project = %+v", p)
	}

	// Re-upsert same identity returns same id.
	id2, err := s.EnsureProjectIdentity(ctx, "git:r1", "myrepo", "/path/other", "other-model", "git", 0)
	if err != nil {
		t.Fatalf("EnsureProjectIdentity re-upsert: %v", err)
	}
	if id2 != id {
		t.Errorf("re-upsert changed id %d -> %d", id, id2)
	}

	// Lookup unknown identity -> ErrNotFound.
	if _, err := s.GetProjectByIdentity(ctx, "nonexistent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProjectByIdentity(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestSetWorktreeFilesAndPrune exercises SetWorktreeFiles (full and empty
// maps) and PruneUnreferencedFiles, then checks the count via CountProjectFiles.
func TestSetWorktreeFilesAndPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	projectID, err := s.UpsertProject(ctx, "prune-test", "/tmp/p", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// Insert some files.
	_, _ = s.UpsertFile(ctx, projectID, "a.go", "h1", 10)
	_, _ = s.UpsertFile(ctx, projectID, "b.go", "h2", 20)
	_, _ = s.UpsertFile(ctx, projectID, "c.go", "h3", 30) // not in worktree

	// Set worktree manifest.
	if err := s.SetWorktreeFiles(ctx, projectID, "main", map[string]string{
		"a.go": "h1",
		"b.go": "h2",
	}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}

	// Prune: c.go has no worktree reference, so it should be removed.
	n, err := s.PruneUnreferencedFiles(ctx, projectID)
	if err != nil {
		t.Fatalf("PruneUnreferencedFiles: %v", err)
	}
	if n != 1 {
		t.Errorf("PruneUnreferencedFiles removed %d, want 1", n)
	}

	// CountProjectFiles should report 2 remaining.
	count, err := s.CountProjectFiles(ctx, projectID)
	if err != nil {
		t.Fatalf("CountProjectFiles: %v", err)
	}
	if count != 2 {
		t.Errorf("CountProjectFiles = %d, want 2", count)
	}

	// SetWorktreeFiles with empty map should clear but not error.
	if err := s.SetWorktreeFiles(ctx, projectID, "main", map[string]string{}); err != nil {
		t.Fatalf("SetWorktreeFiles(empty): %v", err)
	}

	// Empty worktree + prune removes everything.
	n2, _ := s.PruneUnreferencedFiles(ctx, projectID)
	if n2 != 2 {
		t.Errorf("after empty worktree, prune removed %d, want 2", n2)
	}

	// Count should be 0 now.
	if count, _ := s.CountProjectFiles(ctx, projectID); count != 0 {
		t.Errorf("CountProjectFiles after full prune = %d, want 0", count)
	}
}

// TestInsertFileDependenciesAndFetchGraph covers InsertFileDependencies
// (including empty targets) and FetchGraphNeighbors round-trip.
func TestInsertFileDependenciesAndFetchGraph(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	projectID, _ := s.UpsertProject(ctx, "dep-test", "/tmp/dep", "bge-m3", 0)

	// Insert dependencies.
	if err := s.InsertFileDependencies(ctx, projectID, "main.go", []string{"util.go", "config.go"}); err != nil {
		t.Fatalf("InsertFileDependencies: %v", err)
	}

	// Fetch graph.
	graph, err := s.FetchGraphNeighbors(ctx, projectID)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors: %v", err)
	}
	if len(graph) != 1 {
		t.Errorf("graph has %d source files, want 1", len(graph))
	}
	if len(graph["main.go"]) != 2 {
		t.Errorf("main.go has %d deps, want 2", len(graph["main.go"]))
	}

	// Replace deps for same source file — should delete old and insert new.
	if err := s.InsertFileDependencies(ctx, projectID, "main.go", []string{"util.go"}); err != nil {
		t.Fatalf("InsertFileDependencies(update): %v", err)
	}
	graph, _ = s.FetchGraphNeighbors(ctx, projectID)
	if len(graph["main.go"]) != 1 || graph["main.go"][0] != "util.go" {
		t.Errorf("after update: %v", graph["main.go"])
	}

	// Empty targets clears deps for that source.
	if err := s.InsertFileDependencies(ctx, projectID, "main.go", nil); err != nil {
		t.Fatalf("InsertFileDependencies(empty): %v", err)
	}
	graph, _ = s.FetchGraphNeighbors(ctx, projectID)
	if _, ok := graph["main.go"]; ok {
		t.Errorf("main.go still has deps after clearing: %v", graph)
	}

	// Empty project has no neighbors.
	empty, err := s.FetchGraphNeighbors(ctx, 99999)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors(unknown project): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("unknown project graph = %v, want empty", empty)
	}
}

// TestFetchChunksByPathAndDirPrefix covers FetchChunksByPath,
// FetchChunksByDirPrefix, and limit/empty edge cases.
func TestFetchChunksByPathAndDirPrefix(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	projectID, _ := s.UpsertProject(ctx, "fetch-test", "/tmp/fetch", "bge-m3", 0)

	fid1, _ := s.UpsertFile(ctx, projectID, "src/a.go", "h1", 10)
	fid2, _ := s.UpsertFile(ctx, projectID, "src/b.go", "h2", 10)
	fid3, _ := s.UpsertFile(ctx, projectID, "test/a_test.go", "h3", 10)

	chunks1 := []chunker.Chunk{
		{Content: "package a", StartLine: 1, EndLine: 1},
		{Content: "func A() {}", StartLine: 3, EndLine: 5},
	}
	chunks2 := []chunker.Chunk{{Content: "package b", StartLine: 1, EndLine: 1}}
	chunks3 := []chunker.Chunk{{Content: "package test", StartLine: 1, EndLine: 1}}

	_ = s.InsertChunks(ctx, projectID, fid1, chunks1, [][]float32{{1, 0, 0}, {0, 1, 0}}, 3)
	_ = s.InsertChunks(ctx, projectID, fid2, chunks2, [][]float32{{0, 0, 1}}, 3)
	_ = s.InsertChunks(ctx, projectID, fid3, chunks3, [][]float32{{1, 1, 0}}, 3)

	// Fetch by path returns all chunks for one file.
	byPath, err := s.FetchChunksByPath(ctx, projectID, "src/a.go", 3, 10)
	if err != nil {
		t.Fatalf("FetchChunksByPath: %v", err)
	}
	if len(byPath) != 2 {
		t.Errorf("FetchChunksByPath returned %d, want 2", len(byPath))
	}
	if byPath[0].Content != "package a" {
		t.Errorf("first chunk content = %q, want %q", byPath[0].Content, "package a")
	}
	if byPath[0].StartLine != 1 || byPath[0].EndLine != 1 {
		t.Errorf("first chunk line range = [%d,%d], want [1,1]", byPath[0].StartLine, byPath[0].EndLine)
	}
	// Score for by-path queries is always 0.5 (constant).
	if byPath[0].Score != 0.5 {
		t.Errorf("by-path score = %f, want 0.5", byPath[0].Score)
	}

	// Fetch by dir prefix returns chunks for all files in that dir.
	byDir, err := s.FetchChunksByDirPrefix(ctx, projectID, "src/", 3, 10)
	if err != nil {
		t.Fatalf("FetchChunksByDirPrefix: %v", err)
	}
	if len(byDir) != 3 { // 2 for a.go + 1 for b.go
		t.Errorf("FetchChunksByDirPrefix(src/) returned %d, want 3", len(byDir))
	}

	// Limit applies.
	byDirLimit, err := s.FetchChunksByDirPrefix(ctx, projectID, "src/", 3, 2)
	if err != nil {
		t.Fatalf("FetchChunksByDirPrefix(limit=2): %v", err)
	}
	if len(byDirLimit) != 2 {
		t.Errorf("FetchChunksByDirPrefix(limit=2) returned %d, want 2", len(byDirLimit))
	}

	// Unknown path returns empty.
	empty, err := s.FetchChunksByPath(ctx, projectID, "unknown.go", 3, 10)
	if err != nil || len(empty) != 0 {
		t.Errorf("FetchChunksByPath(unknown) = %d, err %v; want 0", len(empty), err)
	}
}

// TestSearchSimilarWorktree verifies vector search scoped to a worktree's
// checked-out file versions via the worktree_files join.
func TestSearchSimilarWorktree(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	projectID, _ := s.UpsertProject(ctx, "wt-test", "/tmp/wt", "bge-m3", 0)

	fid1, _ := s.UpsertFile(ctx, projectID, "v1.go", "hash-v1", 10)
	fid2, _ := s.UpsertFile(ctx, projectID, "v2.go", "hash-v2", 10)

	_ = s.InsertChunks(ctx, projectID, fid1, []chunker.Chunk{{Content: "version one", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0, 0}}, 3)
	_ = s.InsertChunks(ctx, projectID, fid2, []chunker.Chunk{{Content: "version two", StartLine: 1, EndLine: 1}}, [][]float32{{0, 1, 0}}, 3)

	// Set worktree manifest for "main" containing only v1.go.
	if err := s.SetWorktreeFiles(ctx, projectID, "main", map[string]string{"v1.go": "hash-v1"}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}

	// SearchSimilarWorktree scoped to "main" only returns v1.go content.
	results, err := s.SearchSimilarWorktree(ctx, projectID, []float32{1, 0, 0}, 3, 10, "main")
	if err != nil {
		t.Fatalf("SearchSimilarWorktree: %v", err)
	}
	if len(results) != 1 || results[0].Content != "version one" {
		t.Errorf("SearchSimilarWorktree = %+v, want version one only", results)
	}
	if results[0].FilePath != "v1.go" {
		t.Errorf("SearchSimilarWorktree file = %q, want v1.go", results[0].FilePath)
	}

	// Unrestricted SearchSimilar returns both (no worktree join).
	all, err := s.SearchSimilar(ctx, projectID, []float32{1, 0, 0}, 3, 10)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("SearchSimilar = %d, want 2", len(all))
	}
}

// TestSearchSimilarKeywordsWorktree verifies keyword search scoped to a
// worktree's checked-out file versions.
func TestSearchSimilarKeywordsWorktree(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	projectID, _ := s.UpsertProject(ctx, "kw-wt", "/tmp/kw-wt", "bge-m3", 0)

	fid1, _ := s.UpsertFile(ctx, projectID, "auth.go", "h1", 10)
	fid2, _ := s.UpsertFile(ctx, projectID, "other.go", "h2", 10)

	_ = s.InsertChunks(ctx, projectID, fid1, []chunker.Chunk{{Content: "authenticate user", StartLine: 5, EndLine: 5}}, [][]float32{{1, 0, 0}}, 3)
	_ = s.InsertChunks(ctx, projectID, fid2, []chunker.Chunk{{Content: "authenticate admin", StartLine: 10, EndLine: 10}}, [][]float32{{0, 1, 0}}, 3)

	if err := s.SetWorktreeFiles(ctx, projectID, "main", map[string]string{"auth.go": "h1"}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}

	// Worktree-scoped keyword search should only match auth.go.
	kw, err := s.SearchSimilarKeywordsWorktree(ctx, projectID, "authenticate", 3, 10, "main")
	if err != nil {
		t.Fatalf("SearchSimilarKeywordsWorktree: %v", err)
	}
	if len(kw) != 1 || kw[0].FilePath != "auth.go" {
		t.Errorf("worktree keyword search = %+v, want auth.go only", kw)
	}
	if kw[0].StartLine != 5 {
		t.Errorf("worktree keyword start line = %d, want 5", kw[0].StartLine)
	}

	// Unrestricted keyword search returns both.
	all, _ := s.SearchSimilarKeywords(ctx, projectID, "authenticate", 3, 10)
	if len(all) != 2 {
		t.Errorf("unrestricted keyword search = %d, want 2", len(all))
	}
}

// TestEnqueueBatchJob verifies a job with a JSON payload is queued and
// can be retrieved with the correct type and payload.
func TestEnqueueBatchJob(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, _ := s.CreateProject(ctx, "batch", "bge-m3", "push", "", "", 0)

	id, err := s.EnqueueBatchJob(ctx, p.ID, `{"files":["a.go","b.go"]}`)
	if err != nil {
		t.Fatalf("EnqueueBatchJob: %v", err)
	}

	job, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Type != "batch" || job.Status != "queued" || job.Payload != `{"files":["a.go","b.go"]}` {
		t.Errorf("batch job = %+v", job)
	}
	if job.ProjectID != p.ID {
		t.Errorf("job.ProjectID = %d, want %d", job.ProjectID, p.ID)
	}
}

// TestResolveDimsEdgeCases covers resolveDims — the fallback chain from
// GetProjectByID → probeDimsForProject → hard-coded default 1024.
func TestResolveDimsEdgeCases(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create a project with dims=768.
	pid, _ := s.UpsertProject(ctx, "dim-proj", "/tmp/dim", "bge-m3", 768)

	// resolveDims with dims=-1 should fall back to the project's dims
	// (768 from the upsert, since upsert uses identity=name so the
	// CONFLICT path sets dims=768).
	d := s.resolveDims(ctx, pid, -1)
	if d != 768 {
		t.Errorf("resolveDims(-1) = %d, want 768", d)
	}

	// resolveDims with a positive dims should keep it.
	d2 := s.resolveDims(ctx, pid, 1024)
	if d2 != 1024 {
		t.Errorf("resolveDims(1024) = %d, want 1024", d2)
	}
}

// TestProbeDimsForProject verifies that probing scans existing chunks_* tables
// for project data. This also exercises the information_schema query path.
func TestProbeDimsForProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	projectID, _ := s.UpsertProject(ctx, "probe-test", "/tmp/p", "bge-m3", 0)

	// Insert chunks with dims=3.
	fid, _ := s.UpsertFile(ctx, projectID, "x.go", "h1", 10)
	_ = s.InsertChunks(ctx, projectID, fid, []chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, [][]float32{{1, 0, 0}}, 3)

	// Probe should find dims=3.
	d := s.probeDimsForProject(ctx, projectID)
	if d != 3 {
		t.Errorf("probeDimsForProject = %d, want 3", d)
	}

	// For a project with no chunks, probe should return 0.
	noChunksPID, _ := s.UpsertProject(ctx, "empty-proj", "/tmp/e", "bge-m3", 0)
	d2 := s.probeDimsForProject(ctx, noChunksPID)
	if d2 != 0 {
		t.Errorf("probeDimsForProject(empty) = %d, want 0", d2)
	}
}

// TestGetProjectByIDNotFound verifies that GetProjectByID returns ErrNotFound
// for a non-existent ID.
func TestGetProjectByIDNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.GetProjectByID(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetProjectByID(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestFailJob verifies the FailJob path for completeness.
func TestFailJob(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, _ := s.CreateProject(ctx, "fail-me", "bge-m3", "push", "", "", 0)
	id, _ := s.EnqueueJob(ctx, p.ID, "full")

	if err := s.FailJob(ctx, id, "something went wrong"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	job, _ := s.GetJob(ctx, id)
	if job.Status != "failed" || job.Error != "something went wrong" {
		t.Errorf("failed job = %+v", job)
	}
}

// TestPing verifies that Ping returns nil on a live connection.
func TestPing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// TestDistanceExpr covers both the normal and high-dimensional paths of
// distanceExpr (pure function, no container needed).
func TestDistanceExpr(t *testing.T) {
	// Normal dimensions (<= hnswVectorLimit).
	expr := distanceExpr(1024)
	if expr != "c.embedding <=> $1" {
		t.Errorf("distanceExpr(1024) = %q, want c.embedding <=> $1", expr)
	}

	// High dimensions (> hnswVectorLimit) use halfvec cast.
	exprHigh := distanceExpr(3000)
	want := "c.embedding::halfvec(3000) <=> $1::halfvec(3000)"
	if exprHigh != want {
		t.Errorf("distanceExpr(3000) = %q, want %q", exprHigh, want)
	}
}

// TestSetUserPasswordNotFound covers the ErrNotFound path in SetUserPassword.
func TestSetUserPasswordNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetUserPassword(ctx, 99999, "hash"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetUserPassword(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestSetUserDisabledNotFound covers the ErrNotFound path in SetUserDisabled.
func TestSetUserDisabledNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetUserDisabled(ctx, 99999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetUserDisabled(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestDeleteExpiredSessionsNoExpired verifies that DeleteExpiredSessions
// returns 0 when there are no expired sessions.
func TestDeleteExpiredSessionsNoExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, _ := s.CreateUser(ctx, "alice", "hash", "member")
	_ = s.CreateSession(ctx, "tok", u.ID, time.Now().UTC().Add(time.Hour))

	n, err := s.DeleteExpiredSessions(ctx)
	if err != nil || n != 0 {
		t.Errorf("DeleteExpiredSessions = %d, err %v; want 0", n, err)
	}
}

// TestListUsersPagination covers the limit/offset behaviour of ListUsers.
func TestListUsersPagination(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("user%d", i)
		if _, err := s.CreateUser(ctx, name, "hash", "member"); err != nil {
			t.Fatalf("CreateUser(%s): %v", name, err)
		}
	}

	// No limit returns all.
	all, err := s.ListUsers(ctx, 0, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListUsers(all) = %d err=%v, want 3", len(all), err)
	}

	// With limit.
	paginated, err := s.ListUsers(ctx, 2, 0)
	if err != nil || len(paginated) != 2 {
		t.Fatalf("ListUsers(limit=2) = %d err=%v, want 2", len(paginated), err)
	}

	// Offset skips.
	offset, err := s.ListUsers(ctx, 1, 2)
	if err != nil || len(offset) != 1 || offset[0].Username != "user2" {
		t.Fatalf("ListUsers(limit=1, offset=2) = %+v err=%v", offset, err)
	}
}

// TestRevokeUserTokenNotFound covers the ErrNotFound path in cross-user revoke.
func TestRevokeUserTokenNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, _ := s.CreateUser(ctx, "test", "hash", "member")
	if err := s.RevokeUserToken(ctx, u.ID, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("RevokeUserToken(unknown) err = %v, want ErrNotFound", err)
	}
}

// TestDeleteProjectCascade verifies that deleting a project cascades to
// its files via the ON DELETE CASCADE foreign key.
func TestDeleteProjectCascade(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	projectID, _ := s.UpsertProject(ctx, "cascade-del", "/tmp/cd", "bge-m3", 0)
	fid, _ := s.UpsertFile(ctx, projectID, "a.go", "h1", 10)
	_ = s.InsertChunks(ctx, projectID, fid, []chunker.Chunk{{Content: "x"}}, [][]float32{{1, 0, 0}}, 3)

	// Delete via CreateProject path name (different identity from UpsertProject).
	// UpsertProject uses identity=name, so the name and identity are the same.
	if err := s.DeleteProject(ctx, "cascade-del"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	// Files should be gone (cascade).
	if count, _ := s.CountProjectFiles(ctx, projectID); count != 0 {
		t.Errorf("files remain after project delete: %d", count)
	}
}

// TestChunksTableInvalidDims verifies EnsureChunksTable returns an error
// for invalid dimensions.
func TestChunksTableInvalidDims(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 0); err == nil {
		t.Error("EnsureChunksTable(0) should error")
	}
	if err := s.EnsureChunksTable(ctx, -1); err == nil {
		t.Error("EnsureChunksTable(-1) should error")
	}
	if err := s.EnsureChunksTable(ctx, maxDims+1); err == nil {
		t.Error("EnsureChunksTable(maxDims+1) should error")
	}
}

func TestProjectCommitRoundTripPg(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	projectID, err := s.UpsertProject(ctx, "commit-pg", "/tmp/c", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if sha, err := s.GetProjectCommit(ctx, projectID); err != nil || sha != "" {
		t.Fatalf("initial commit = %q err=%v", sha, err)
	}
	if err := s.UpdateProjectCommit(ctx, projectID, "cafebabe"); err != nil {
		t.Fatalf("UpdateProjectCommit: %v", err)
	}
	if sha, err := s.GetProjectCommit(ctx, projectID); err != nil || sha != "cafebabe" {
		t.Fatalf("commit = %q err=%v", sha, err)
	}
}

func TestFetchGraphPathsBFSPg(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	projectID, err := s.UpsertProject(ctx, "bfs-pg", "/tmp/bfs", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.InsertFileDependencies(ctx, projectID, "main.go", []string{"util.go"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertFileDependencies(ctx, projectID, "util.go", []string{"config.go"}); err != nil {
		t.Fatal(err)
	}

	paths, err := s.FetchGraphPathsBFS(ctx, projectID, []string{"main.go"}, 2)
	if err != nil {
		t.Fatalf("FetchGraphPathsBFS: %v", err)
	}
	if len(paths) < 1 {
		t.Fatalf("paths = %+v", paths)
	}

	if got, err := s.FetchGraphPathsBFS(ctx, projectID, nil, 2); err != nil || got != nil {
		t.Fatalf("empty seeds = %+v err=%v", got, err)
	}
}

// TestListenJobInsert verifies the LISTEN/NOTIFY channel receives notifications
// sent via pg_notify.
func TestListenJobInsert(t *testing.T) {
	s := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := s.ListenJobInsert(ctx)
	if err != nil {
		t.Fatalf("ListenJobInsert: %v", err)
	}

	// Send a notification via pg_notify.
	if _, err := s.pool.Exec(ctx, `SELECT pg_notify('job_inserted', '42')`); err != nil {
		t.Fatalf("pg_notify: %v", err)
	}

	select {
	case payload := <-ch:
		if payload != "42" {
			t.Fatalf("got payload %q, want %q", payload, "42")
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for notification")
	}
}

// TestUpdateJobProgressAndComplete verifies UpdateJobProgress and the
// progress_done/progress_total/error_count fields on a running job.
func TestUpdateJobProgressAndComplete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, _ := s.CreateProject(ctx, "progress-test", "bge-m3", "push", "", "", 0)

	// Claim the auto-queued job (EnqueueJob enqueues with status=queued).
	id, err := s.EnqueueJob(ctx, p.ID, "full")
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Claim it so status becomes 'running'.
	claimed, err := s.ClaimJob(ctx)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimJob: %v / nil", err)
	}

	// Update progress.
	if err := s.UpdateJobProgress(ctx, id, 5, 10, 3, 7, 1); err != nil {
		t.Fatalf("UpdateJobProgress: %v", err)
	}
	job, _ := s.GetJob(ctx, id)
	if job.ProgressDone != 5 || job.ProgressTotal != 10 || job.FilesIndexed != 3 || job.ChunksCreated != 7 || job.ErrorCount != 1 {
		t.Errorf("after progress update: %+v", job)
	}

	// UpdateProgress on a non-running job (already claimed+updated) — still running.
	if err := s.UpdateJobProgress(ctx, id, 8, 0, 4, 9, 0); err != nil {
		t.Fatalf("UpdateJobProgress(2nd): %v", err)
	}
	job, _ = s.GetJob(ctx, id)
	if job.ProgressDone != 8 || job.ProgressTotal != 10 {
		// progress_total should remain 10 because $3=0 skips the update
		t.Errorf("after 2nd update: progress_done=%d total=%d (want 8,10)", job.ProgressDone, job.ProgressTotal)
	}

	// Complete the job.
	if err := s.CompleteJob(ctx, id, 4, 9, 0, 0); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	job, _ = s.GetJob(ctx, id)
	if job.Status != "succeeded" || job.FilesIndexed != 4 || job.ChunksCreated != 9 {
		t.Errorf("completed job = %+v", job)
	}
}

// TestListRecentJobs covers pagination, multi-project listing, and limit
// clamping in ListRecentJobs.
func TestListRecentJobs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	p, _ := s.CreateProject(ctx, "list-jobs", "bge-m3", "push", "", "", 0)

	// Enqueue two jobs.
	id1, _ := s.EnqueueJob(ctx, p.ID, "full")
	id2, _ := s.EnqueueJob(ctx, p.ID, "git_history")

	// List recent (project-scoped).
	jobs, err := s.ListRecentJobs(ctx, p.ID, 10)
	if err != nil {
		t.Fatalf("ListRecentJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("ListRecentJobs returned %d, want 2", len(jobs))
	}
	// Newest first.
	if jobs[0].ID != id2 || jobs[1].ID != id1 {
		t.Errorf("order: got ids %d,%d; want %d,%d", jobs[0].ID, jobs[1].ID, id2, id1)
	}

	// Limit clamping (capped at 50).
	jobs, err = s.ListRecentJobs(ctx, p.ID, 100)
	if err != nil || len(jobs) != 2 {
		t.Errorf("ListRecentJobs(limit=100) = %d err=%v; want 2", len(jobs), err)
	}

	// List across all projects (projectID=0).
	all, err := s.ListRecentJobs(ctx, 0, 1)
	if err != nil {
		t.Fatalf("ListRecentJobs(all): %v", err)
	}
	if len(all) <= 0 {
		t.Errorf("ListRecentJobs(all) returned 0 jobs")
	}
}

// TestCountProjectChunks covers counting chunks per project, including the
// resolveDims auto-detection path.
func TestCountProjectChunks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	projectID, _ := s.UpsertProject(ctx, "count-chunks", "/tmp/c", "bge-m3", 0)

	fid, _ := s.UpsertFile(ctx, projectID, "a.go", "h1", 10)
	_ = s.InsertChunks(ctx, projectID, fid, []chunker.Chunk{{Content: "x"}}, [][]float32{{1, 0, 0}}, 3)

	// Count with explicit dims.
	n, err := s.CountProjectChunks(ctx, projectID, 3)
	if err != nil || n != 1 {
		t.Fatalf("CountProjectChunks(dims=3) = %d err=%v; want 1", n, err)
	}

	// Count with auto-resolve (dims <= 0).
	n2, err := s.CountProjectChunks(ctx, projectID, -1)
	if err != nil || n2 != 1 {
		t.Fatalf("CountProjectChunks(auto) = %d err=%v; want 1", n2, err)
	}

	// Invalid dims produces an error.
	_, err = s.CountProjectChunks(ctx, projectID, maxDims+1)
	if err == nil {
		t.Error("CountProjectChunks(invalid dims) should error")
	}
}

func TestEmbeddingCacheRoundTripPg(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	dims := 3
	if err := s.EnsureEmbeddingCacheTable(ctx, dims); err != nil {
		t.Fatal(err)
	}
	hashes := []string{"hash-a", "hash-b"}
	embs := [][]float32{{1, 0, 0}, {0, 1, 0}}
	if err := s.InsertEmbeddingCache(ctx, hashes, "bge-m3", embs, dims); err != nil {
		t.Fatalf("InsertEmbeddingCache: %v", err)
	}
	got, err := s.LookupEmbeddingCache(ctx, hashes, "bge-m3", dims)
	if err != nil {
		t.Fatalf("LookupEmbeddingCache: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("cache = %+v", got)
	}
	n, err := s.PruneEmbeddingCache(ctx, dims)
	if err != nil || n != 2 {
		t.Fatalf("PruneEmbeddingCache = %d err=%v", n, err)
	}
}

// TestEmbeddingCacheTableValidation covers valid and invalid dimension ranges
// for the embeddingCacheTable pure function.
func TestEmbeddingCacheTableValidation(t *testing.T) {
	t.Run("valid dims", func(t *testing.T) {
		tbl, err := embeddingCacheTable(3)
		if err != nil || tbl == "" {
			t.Fatalf("embeddingCacheTable(3) = %q err=%v", tbl, err)
		}
	})
	t.Run("zero dims", func(t *testing.T) {
		_, err := embeddingCacheTable(0)
		if err == nil {
			t.Error("embeddingCacheTable(0) should error")
		}
	})
	t.Run("too large", func(t *testing.T) {
		_, err := embeddingCacheTable(maxDims + 1)
		if err == nil {
			t.Error("embeddingCacheTable(maxDims+1) should error")
		}
	})
	t.Run("negative dims", func(t *testing.T) {
		_, err := embeddingCacheTable(-1)
		if err == nil {
			t.Error("embeddingCacheTable(-1) should error")
		}
	})
}

// TestDeleteExpiredSessionsWithExpired covers the case where sessions exist
// that have already expired, so RowsAffected > 0.
func TestDeleteExpiredSessionsWithExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, _ := s.CreateUser(ctx, "bob", "hash", "member")
	_ = s.CreateSession(ctx, "valid-tok", u.ID, time.Now().UTC().Add(time.Hour))
	_ = s.CreateSession(ctx, "expired-tok", u.ID, time.Now().UTC().Add(-time.Hour))

	n, err := s.DeleteExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if n < 1 {
		t.Errorf("DeleteExpiredSessions returned %d, want >=1 expired session removed", n)
	}

	// Run again — should return 0 since the expired session is already gone.
	n2, err := s.DeleteExpiredSessions(ctx)
	if err != nil || n2 != 0 {
		t.Errorf("second DeleteExpiredSessions = %d err=%v; want 0", n2, err)
	}
}

// TestListMessagesWithLimit verifies ListMessages with a limit and over-limit
// clamping behaviour.
func TestListMessagesWithLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "msg-limit-user", "hash", "member")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	conv, err := s.CreateConversation(ctx, u.ID, "acme", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.AddMessage(ctx, conv.ID, "user", fmt.Sprintf("msg %d", i), ""); err != nil {
			t.Fatalf("AddMessage(%d): %v", i, err)
		}
	}

	// No limit returns all.
	all, err := s.ListMessages(ctx, conv.ID, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListMessages(no limit) = %d err=%v; want 3", len(all), err)
	}

	// Limit returns fewer.
	limited, err := s.ListMessages(ctx, conv.ID, 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("ListMessages(limit=2) = %d err=%v; want 2", len(limited), err)
	}

	// Unknown conversation returns empty.
	empty, err := s.ListMessages(ctx, 99999, 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ListMessages(unknown) = %d err=%v; want 0", len(empty), err)
	}
}

// TestPruneUnreferencedFilesNoop verifies that PruneUnreferencedFiles returns 0
// when every file has a matching worktree entry.
func TestPruneUnreferencedFilesNoop(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pid, _ := s.UpsertProject(ctx, "prune-noop", "/p", "bge-m3", 0)
	fid, _ := s.UpsertFile(ctx, pid, "keep.go", "h", 10)
	_ = s.InsertChunks(ctx, pid, fid, []chunker.Chunk{{Content: "x"}}, [][]float32{{1, 0, 0}}, 3)

	// Reference the file in a worktree so prune finds nothing to remove.
	if err := s.SetWorktreeFiles(ctx, pid, "main", map[string]string{"keep.go": "h"}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}
	n, err := s.PruneUnreferencedFiles(ctx, pid)
	if err != nil || n != 0 {
		t.Fatalf("PruneUnreferencedFiles = %d, err %v; want 0", n, err)
	}
}
