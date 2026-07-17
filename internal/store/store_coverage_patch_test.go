package store

import (
	"context"
	"errors"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
)

// TestPoolStatsNonZeroAfterActivity exercises PoolStats after real DB work.
// coverage-patch: 2026-07-17
func TestPoolStatsNonZeroAfterActivity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.UpsertProject(ctx, "pool-stats", "/tmp/ps", "bge-m3", 0); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	st := s.PoolStats()
	if st.MaxConns <= 0 {
		t.Fatalf("PoolStats.MaxConns = %d, want > 0", st.MaxConns)
	}
	if st.TotalConns < 0 {
		t.Fatalf("PoolStats.TotalConns = %d, want >= 0", st.TotalConns)
	}
	// MaxConns is forced to at least 16 in NewPgStore.
	if st.MaxConns < 16 {
		t.Fatalf("PoolStats.MaxConns = %d, want >= 16", st.MaxConns)
	}
}

// TestNewPgStoreInvalidDSN returns an error without hanging.
// coverage-patch: 2026-07-17
func TestNewPgStoreInvalidDSN(t *testing.T) {
	ctx := context.Background()

	if _, err := NewPgStore(ctx, "not-a-valid-dsn"); err == nil {
		t.Fatal("NewPgStore(invalid) = nil error, want rejection")
	}
	if _, err := NewPgStore(ctx, "://broken"); err == nil {
		t.Fatal("NewPgStore(broken scheme) = nil error, want rejection")
	}
	// Parseable but unreachable: fails on ping/connect.
	if _, err := NewPgStore(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"); err == nil {
		t.Fatal("NewPgStore(unreachable) = nil error, want rejection")
	}
}

// TestGetProjectNotFound covers the name lookup ErrNotFound path.
// coverage-patch: 2026-07-17
func TestGetProjectNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.GetProject(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject(missing) err = %v, want ErrNotFound", err)
	}
}

// TestPruneUnreferencedFilesWithOrphans inserts files, prunes those without
// worktree refs, and checks CountProjectFiles.
// coverage-patch: 2026-07-17
func TestPruneUnreferencedFilesWithOrphans(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	pid, err := s.UpsertProject(ctx, "prune-orphans", "/tmp/po", "bge-m3", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	_, _ = s.UpsertFile(ctx, pid, "live.go", "hl", 10)
	fidOrphan, _ := s.UpsertFile(ctx, pid, "orphan.go", "ho", 20)
	_ = s.InsertChunks(ctx, pid, fidOrphan, []chunker.Chunk{{Content: "dead"}}, [][]float32{{1, 0, 0}}, 3)
	_ = s.InsertFileDependencies(ctx, pid, "orphan.go", []string{"live.go"})

	if err := s.SetWorktreeFiles(ctx, pid, "main", map[string]string{"live.go": "hl"}); err != nil {
		t.Fatalf("SetWorktreeFiles: %v", err)
	}

	n, err := s.PruneUnreferencedFiles(ctx, pid)
	if err != nil {
		t.Fatalf("PruneUnreferencedFiles: %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneUnreferencedFiles removed %d, want 1", n)
	}
	count, err := s.CountProjectFiles(ctx, pid)
	if err != nil || count != 1 {
		t.Fatalf("CountProjectFiles = %d err=%v, want 1", count, err)
	}

	// Clear worktree and prune the last file.
	if err := s.SetWorktreeFiles(ctx, pid, "main", map[string]string{}); err != nil {
		t.Fatalf("SetWorktreeFiles(empty): %v", err)
	}
	n2, err := s.PruneUnreferencedFiles(ctx, pid)
	if err != nil || n2 != 1 {
		t.Fatalf("second prune = %d err=%v, want 1", n2, err)
	}
}

// TestCloseThenPingFails opens a dedicated pool (not the shared test store),
// closes it, and checks Ping fails safely.
// coverage-patch: 2026-07-17
func TestCloseThenPingFails(t *testing.T) {
	// Ensure the shared container is up so we can open a second pool.
	shared := newTestStore(t)
	ctx := context.Background()

	dsn := shared.pool.Config().ConnString()
	if dsn == "" {
		t.Skip("empty ConnString from shared pool")
	}

	s2, err := NewPgStore(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPgStore(second pool): %v", err)
	}
	if err := s2.Ping(ctx); err != nil {
		t.Fatalf("Ping before Close: %v", err)
	}
	st := s2.PoolStats()
	if st.MaxConns < 1 {
		t.Fatalf("PoolStats before Close: %+v", st)
	}

	s2.Close()
	// Closed pool must not panic; Ping should error.
	if err := s2.Ping(ctx); err == nil {
		t.Fatal("Ping after Close = nil, want error")
	}
}

// TestListProjectsAndUpdateProjectStatus covers listing with limit/offset and
// status updates.
// coverage-patch: 2026-07-17
func TestListProjectsAndUpdateProjectStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	idA, _ := s.UpsertProject(ctx, "alpha-list", "/a", "m", 0)
	_, _ = s.UpsertProject(ctx, "beta-list", "/b", "m", 0)
	_, _ = s.UpsertProject(ctx, "gamma-list", "/c", "m", 0)

	all, err := s.ListProjects(ctx, 0, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListProjects(all) = %d err=%v, want 3", len(all), err)
	}
	limited, err := s.ListProjects(ctx, 2, 0)
	if err != nil || len(limited) != 2 {
		t.Fatalf("ListProjects(limit=2) = %d err=%v, want 2", len(limited), err)
	}
	offset, err := s.ListProjects(ctx, 1, 2)
	if err != nil || len(offset) != 1 {
		t.Fatalf("ListProjects(limit=1,offset=2) = %d err=%v, want 1", len(offset), err)
	}

	if err := s.UpdateProjectStatus(ctx, idA, "ready"); err != nil {
		t.Fatalf("UpdateProjectStatus: %v", err)
	}
	p, err := s.GetProject(ctx, "alpha-list")
	if err != nil || p.Status != "ready" {
		t.Fatalf("GetProject after status = %+v err=%v, want ready", p, err)
	}
	// Unknown id: UPDATE matches 0 rows but does not error.
	if err := s.UpdateProjectStatus(ctx, 99999, "ready"); err != nil {
		t.Fatalf("UpdateProjectStatus(missing id): %v", err)
	}
}
