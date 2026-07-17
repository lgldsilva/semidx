package mcpserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// mustRun runs a command in dir, failing the test on error.
func mustRun(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, string(out))
	}
	return string(out)
}

func TestLocalBackendCapabilities(t *testing.T) {
	// Default capabilities when no flags are passed.
	b := NewLocalBackend(nil, nil, false, agent.Capabilities{})
	caps := b.Capabilities()
	if caps.Flags&agent.CapLocalGit == 0 {
		t.Error("expected CapLocalGit in default caps")
	}
	if caps.Flags&agent.CapIndexLocal == 0 {
		t.Error("expected CapIndexLocal in default caps")
	}

	// Explicit capabilities are preserved.
	b2 := NewLocalBackend(nil, nil, false, agent.Capabilities{Flags: agent.CapIndexLocal})
	caps2 := b2.Capabilities()
	if caps2.Flags&agent.CapIndexLocal == 0 {
		t.Error("expected CapIndexLocal in explicit caps")
	}
	if caps2.Flags&agent.CapLocalGit != 0 {
		t.Error("did not expect CapLocalGit in explicit caps")
	}
}

func TestLocalBackendStatusFallbackByIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	// Create a project with a known identity but no direct name match.
	_, err = st.UpsertProject(ctx, "myproject", t.TempDir(), "m", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Get the project to see what identity was assigned.
	proj, err := st.GetProject(ctx, "myproject")
	if err != nil {
		t.Fatal(err)
	}

	b := NewLocalBackend(search.NewService(st, nil), st, false, agent.Capabilities{})
	// Status with the project name should succeed.
	info, err := b.Status(ctx, "myproject")
	if err != nil {
		t.Fatalf("Status(name) failed: %v", err)
	}
	if info.Name != "myproject" {
		t.Errorf("info.Name = %q, want myproject", info.Name)
	}

	// Status with the identity should also succeed (identity fallback).
	info2, err := b.Status(ctx, proj.Identity)
	if err != nil {
		t.Fatalf("Status(identity=%q) failed: %v", proj.Identity, err)
	}
	if info2.Name != "myproject" {
		t.Errorf("info2.Name = %q, want myproject", info2.Name)
	}
}

// createGitRepo initialises a bare-bones git repo at dir and returns the path.
func createGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "git", "init")
	mustRun(t, dir, "git", "config", "user.email", "test@test")
	mustRun(t, dir, "git", "config", "user.name", "Test")
	writeFile(t, dir, "hello.go", "package main\nfunc main() {}\n")
	mustRun(t, dir, "git", "add", ".")
	mustRun(t, dir, "git", "-c", "core.hooksPath=/dev/null", "commit", "-m", "chore: initial")
	// Create a second branch.
	mustRun(t, dir, "git", "branch", "feature-x")
	return dir
}

func TestLocalBackendWorktreesBranchesStatus(t *testing.T) {
	ctx := context.Background()
	repoDir := createGitRepo(t, t.TempDir())

	st, err := localstore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	_, err = st.UpsertProject(ctx, "testrepo", repoDir, "m", 0)
	if err != nil {
		t.Fatal(err)
	}

	b := NewLocalBackend(search.NewService(st, nil), st, false, agent.Capabilities{Flags: agent.CapLocalGit | agent.CapIndexLocal})
	lb := b.(*localBackend)

	// resolveProjectPath — success.
	path, err := lb.resolveProjectPath(ctx, "testrepo")
	if err != nil {
		t.Fatalf("resolveProjectPath(name) failed: %v", err)
	}
	if path != repoDir {
		t.Errorf("path = %q, want %q", path, repoDir)
	}

	// resolveProjectPath — by identity.
	proj, err := st.GetProject(ctx, "testrepo")
	if err != nil {
		t.Fatal(err)
	}
	path2, err := lb.resolveProjectPath(ctx, proj.Identity)
	if err != nil {
		t.Fatalf("resolveProjectPath(identity) failed: %v", err)
	}
	if path2 != repoDir {
		t.Errorf("path2 = %q, want %q", path2, repoDir)
	}

	// resolveProjectPath — not found.
	_, err = lb.resolveProjectPath(ctx, "nonexistent")
	if err == nil {
		t.Fatal("resolveProjectPath(nonexistent) expected error")
	}

	// Worktrees — the main worktree is listed.
	worktrees, err := lb.Worktrees(ctx, "testrepo")
	if err != nil {
		t.Fatalf("Worktrees failed: %v", err)
	}
	if len(worktrees) == 0 {
		t.Fatal("expected at least one worktree")
	}
	if worktrees[0].Path != repoDir {
		t.Errorf("worktree path = %q, want %q", worktrees[0].Path, repoDir)
	}

	// Branches — should have main and feature-x.
	branches, err := lb.Branches(ctx, "testrepo", false)
	if err != nil {
		t.Fatalf("Branches failed: %v", err)
	}
	names := map[string]bool{}
	for _, br := range branches {
		names[br.Name] = true
	}
	if !names["main"] && !names["master"] {
		t.Errorf("expected main or master branch, got %v", names)
	}
	if !names["feature-x"] {
		t.Errorf("expected feature-x branch, got %v", names)
	}

	// GitStatus — should be clean (dirty=false, not detached).
	status, err := lb.GitStatus(ctx, "testrepo")
	if err != nil {
		t.Fatalf("GitStatus failed: %v", err)
	}
	if status.Dirty {
		t.Error("expected clean working tree")
	}
	if status.Detached {
		t.Error("expected not detached")
	}
	if status.CurrentBranch != "main" && status.CurrentBranch != "master" {
		t.Errorf("expected main/master branch, got %q", status.CurrentBranch)
	}
}

func TestLocalBackendWorktreesBranchesStatus_NotFound(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	b := NewLocalBackend(search.NewService(st, nil), st, false, agent.Capabilities{})
	lb := b.(*localBackend)

	// Without any project, methods should fail with "not found".
	_, err = lb.Worktrees(ctx, "nope")
	if err == nil || !strings.Contains(err.Error(), "not found locally") {
		t.Errorf("Worktrees expected 'not found locally', got %v", err)
	}
	_, err = lb.Branches(ctx, "nope", false)
	if err == nil || !strings.Contains(err.Error(), "not found locally") {
		t.Errorf("Branches expected 'not found locally', got %v", err)
	}
	_, err = lb.GitStatus(ctx, "nope")
	if err == nil || !strings.Contains(err.Error(), "not found locally") {
		t.Errorf("GitStatus expected 'not found locally', got %v", err)
	}
}

func TestLocalBackendSearchMulti(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	b := NewLocalBackend(search.NewService(st, nil), st, false, agent.Capabilities{})
	lb := b.(*localBackend)

	resp, err := lb.SearchMulti(ctx, search.MultiScopeRequest{
		Identities: []string{"nope"},
		Query:      "test",
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("SearchMulti failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// KeywordOnly mode.
	bkw := NewLocalBackend(search.NewService(st, nil), st, true, agent.Capabilities{})
	lbkw := bkw.(*localBackend)
	resp2, err := lbkw.SearchMulti(ctx, search.MultiScopeRequest{
		Identities: []string{"nope"},
		Query:      "test",
		TopK:       5,
	})
	if err != nil {
		t.Fatalf("SearchMulti(keyword) failed: %v", err)
	}
	if resp2 == nil {
		t.Fatal("expected non-nil keyword-only response")
	}
}

func TestLocalBackendSearch_Error(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	b := NewLocalBackend(search.NewService(st, nil), st, false, agent.Capabilities{})
	// Search with an empty project name should produce an error.
	_, err = b.Search(ctx, "", "query", "", 5, false, 0)
	if err == nil {
		t.Fatal("expected error for empty project")
	}
}

// TestLocalBackendStatusNotFound verifies that Status returns a clear error
// when neither the project name nor identity resolves.
func TestLocalBackendStatusNotFound(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	b := NewLocalBackend(search.NewService(st, nil), st, false, agent.Capabilities{})
	_, err = b.Status(ctx, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for nonexistent project")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error = %q, want project name in error", err)
	}
}

// TestLocalBackendProjectsError verifies that Projects propagates a store error.
func TestLocalBackendProjectsError(t *testing.T) {
	// Use a closed store to force an error on ListProjects.
	st, err := localstore.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	b := NewLocalBackend(nil, st, false, agent.Capabilities{})
	_, err = b.Projects(context.Background())
	if err == nil {
		t.Fatal("expected error from closed store")
	}
}

// countFilesFailer is a projectLister that returns a valid project from
// GetProject but fails on CountProjectFiles to exercise that error branch.
type countFilesFailer struct {
	store.Project
}

func (m *countFilesFailer) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	return nil, nil
}
func (m *countFilesFailer) GetProject(_ context.Context, name string) (*store.Project, error) {
	if name == m.Name {
		return &m.Project, nil
	}
	return nil, fmt.Errorf("not found")
}
func (m *countFilesFailer) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	return nil, fmt.Errorf("not found")
}
func (m *countFilesFailer) CountProjectFiles(_ context.Context, _ int) (int, error) {
	return 0, fmt.Errorf("simulated db error")
}
func (m *countFilesFailer) ListFileHashes(_ context.Context, _ int) (map[string]string, error) {
	return nil, nil
}

// TestLocalBackendStatusCountFilesError verifies the CountProjectFiles error branch.
func TestLocalBackendStatusCountFilesError(t *testing.T) {
	lb := &localBackend{
		projects: &countFilesFailer{Project: store.Project{Name: "testproj"}},
	}
	_, err := lb.Status(context.Background(), "testproj")
	if err == nil {
		t.Fatal("expected error from CountProjectFiles failure")
	}
	if !strings.Contains(err.Error(), "count files") {
		t.Errorf("error = %q, want 'count files' in error message", err)
	}
}

// emptyPathLister is a projectLister that returns a project with an empty path.
type emptyPathLister struct{}

func (m *emptyPathLister) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	return []store.Project{{Name: "testproj"}}, nil
}
func (m *emptyPathLister) GetProject(_ context.Context, name string) (*store.Project, error) {
	if name == "testproj" {
		return &store.Project{Name: "testproj"}, nil
	}
	return nil, fmt.Errorf("not found")
}
func (m *emptyPathLister) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	return nil, fmt.Errorf("not found")
}
func (m *emptyPathLister) CountProjectFiles(_ context.Context, _ int) (int, error) { return 0, nil }
func (m *emptyPathLister) ListFileHashes(_ context.Context, _ int) (map[string]string, error) {
	return nil, nil
}

// TestLocalBackendResolveProjectPath_EmptyPath verifies the error branch when
// a project exists but has no local path.
func TestLocalBackendResolveProjectPath_EmptyPath(t *testing.T) {
	lb := &localBackend{projects: &emptyPathLister{}}
	_, err := lb.resolveProjectPath(context.Background(), "testproj")
	if err == nil {
		t.Fatal("expected error for empty-path project")
	}
	if !strings.Contains(err.Error(), "no local path") {
		t.Errorf("error = %q, want 'no local path'", err)
	}
}
