package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeListErrStore returns an error from ListProjects to exercise the error
// branch of newListProjectsToolF.
type fakeListErrStore struct {
	store.IndexStore
}

func (fakeListErrStore) ListProjects(_ context.Context, _, _ int) ([]store.Project, error) {
	return nil, errors.New("db unreachable")
}
func (fakeListErrStore) GetProject(_ context.Context, _ string) (*store.Project, error) {
	return nil, store.ErrNotFound
}
func (fakeListErrStore) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	return nil, store.ErrNotFound
}

// findTool returns the tool with the given name, or nil.
func findTool(tools []fantasy.AgentTool, name string) fantasy.AgentTool {
	for _, t := range tools {
		if t.Info().Name == name {
			return t
		}
	}
	return nil
}

func TestReadTools_gatedByDeps(t *testing.T) {
	// No deps → no tools.
	if got := ReadTools(nil, nil, nil); len(got) != 0 {
		t.Errorf("ReadTools(nil,nil,nil) = %d tools, want 0", len(got))
	}
	// Store only → index_status + list_projects.
	got := ReadTools(nil, newFakeSearchStore(), nil)
	if findTool(got, "index_status") == nil || findTool(got, "list_projects") == nil {
		t.Errorf("store-only tools = %v, want index_status + list_projects", toolNames(got))
	}
	if findTool(got, "semantic_search") != nil {
		t.Error("semantic_search must not appear without a search service")
	}
}

func toolNames(tools []fantasy.AgentTool) []string {
	var n []string
	for _, t := range tools {
		n = append(n, t.Info().Name)
	}
	return n
}

func TestReadTools_listProjectsRuns(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "app", Identity: "id-app", SourceType: "git", Status: "ready", Model: "m1",
	})
	tools := ReadTools(nil, fs, nil)

	lp := findTool(tools, "list_projects")
	if lp == nil {
		t.Fatal("list_projects tool not found")
	}
	// Parallel-safe read tool.
	if !lp.Info().Parallel {
		t.Error("list_projects should be marked Parallel")
	}
	resp, err := lp.Run(t.Context(), fantasy.ToolCall{Input: "{}"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.IsError {
		t.Errorf("unexpected error response: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `"app"`) || !strings.Contains(resp.Content, `"id-app"`) {
		t.Errorf("result should list project app/id-app, got: %s", resp.Content)
	}
}

func TestReadTools_indexStatusNoProject(t *testing.T) {
	tools := ReadTools(nil, newFakeSearchStore(), nil)
	is := findTool(tools, "index_status")
	if is == nil {
		t.Fatal("index_status tool not found")
	}
	resp, err := is.Run(t.Context(), fantasy.ToolCall{Input: "{}"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !resp.IsError {
		t.Error("index_status with no project should be a soft error response")
	}
}

// coverage-patch: 2026-07-17 — covers ListProjects error branch of
// newListProjectsToolF (88.9% → 100%)
func TestReadTools_listProjectsFails(t *testing.T) {
	tools := ReadTools(nil, fakeListErrStore{}, nil)
	lp := findTool(tools, "list_projects")
	if lp == nil {
		t.Fatal("list_projects tool not found")
	}
	resp, err := lp.Run(t.Context(), fantasy.ToolCall{Input: "{}"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !resp.IsError {
		t.Error("expected error response when ListProjects fails")
	}
	if !strings.Contains(resp.Content, "list projects failed") {
		t.Errorf("error message mismatch: %s", resp.Content)
	}
}

// coverage-patch: 2026-07-17 — covers index_status found-by-name path (25%)
func TestReadTools_indexStatusFoundByName(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "myapp", Identity: "id-myapp",
		Path: "/tmp/myapp", SourceType: "git", Status: "ready", Model: "m1",
	})
	tools := ReadTools(nil, fs, nil)
	is := findTool(tools, "index_status")
	if is == nil {
		t.Fatal("index_status tool not found")
	}
	resp, err := is.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"myapp"}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.IsError {
		t.Errorf("unexpected error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `"myapp"`) {
		t.Errorf("result should reference myapp, got: %s", resp.Content)
	}
}

// coverage-patch: 2026-07-17 — covers index_status identity fallback (25%)
func TestReadTools_indexStatusFoundByIdentity(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "myapp", Identity: "id-myapp",
		Path: "/tmp/myapp", SourceType: "git", Status: "ready", Model: "m1",
	})
	// Inject project into the store so that by-name lookup for "id-myapp"
	// fails but the identity fallback succeeds.
	tools := ReadTools(nil, fs, nil)
	is := findTool(tools, "index_status")
	if is == nil {
		t.Fatal("index_status tool not found")
	}
	resp, err := is.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"id-myapp"}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.IsError {
		t.Errorf("unexpected error: %s", resp.Content)
	}
	if !strings.Contains(resp.Content, `"myapp"`) {
		t.Errorf("result should reference myapp, got: %s", resp.Content)
	}
}

// coverage-patch: 2026-07-17 — covers index_status project-not-found (25%)
func TestReadTools_indexStatusNotFound(t *testing.T) {
	tools := ReadTools(nil, fakeListErrStore{}, nil)
	is := findTool(tools, "index_status")
	if is == nil {
		t.Fatal("index_status tool not found")
	}
	resp, err := is.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"nope"}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !resp.IsError {
		t.Error("expected error when project is not found")
	}
}

// coverage-patch: 2026-07-17 — covers searchAllProjectsResult error branch
// (87.5% → 100%)
func TestReadTools_searchAllProjectsFails(t *testing.T) {
	svc := search.NewService(fakeListErrStore{}, fakeEmbedder{})
	ctx := ContextWithScope(context.Background(), SearchScope{All: true})
	resp, err := runSemanticSearch(ctx, svc, searchInput{Query: "test", TopK: 5})
	if err != nil {
		t.Fatalf("unexpected raw error (error expected in response): %v", err)
	}
	if !resp.IsError {
		t.Error("expected soft error response when SearchAllProjects fails")
	}
	if !strings.Contains(resp.Content, "search failed") {
		t.Errorf("error message mismatch: %s", resp.Content)
	}
}

// coverage-patch: 2026-07-17 — covers resolveRoot error path for three 0%
// fantasy repo tools.
func TestReadTools_repoToolsResolveFails(t *testing.T) {
	resolver := errScopeResolver{}
	tools := ReadTools(nil, nil, &resolver)
	toolNames := []string{"repo_worktrees", "repo_branches", "repo_status"}
	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			tool := findTool(tools, name)
			if tool == nil {
				t.Fatalf("%s tool not found", name)
			}
			resp, err := tool.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"myproject"}`})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !resp.IsError {
				t.Errorf("expected soft error when resolveRoot fails for %s", name)
			}
		})
	}
}
