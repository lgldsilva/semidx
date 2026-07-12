package agent

import (
	"strings"
	"testing"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/store"
)

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
