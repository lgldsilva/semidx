package agent

import (
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
	semidxclient "github.com/lgldsilva/semidx/pkg/client"
)

// ---------------------------------------------------------------------------
// index_worktree tool
// ---------------------------------------------------------------------------

func TestIndexWorktreeTool_Def(t *testing.T) {
	tool := NewIndexWorktreeTool(nil, nil, PolicyPropose)
	def := tool.Def()
	if def.Name != "index_worktree" {
		t.Errorf("Def().Name = %q, want %q", def.Name, "index_worktree")
	}
	if def.Description == "" {
		t.Error("Def().Description is empty")
	}
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("Def().Parameters[\"properties\"] is not a map")
	}
	if _, ok := props["project"]; !ok {
		t.Error("Def() missing 'project' property")
	}
}

func TestIndexWorktreeTool_Run_invalidJSON(t *testing.T) {
	tool := NewIndexWorktreeTool(nil, nil, PolicyPropose)
	_, err := tool.Run(ctx, `{{{bad}}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("error should mention 'invalid arguments', got: %v", err)
	}
}

func TestIndexWorktreeTool_Run_propose(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "my-project", Identity: "my-id",
		Path: "/tmp/my-project", SourceType: "git", Status: "ready", Model: "test-model",
	})

	tool := NewIndexWorktreeTool(fs, nil, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"my-project"}`)
	if err != nil {
		t.Fatalf("Run(propose): %v", err)
	}
	if !strings.Contains(result, `"proposed":true`) {
		t.Errorf("result should have proposed=true: %s", result)
	}
	if !strings.Contains(result, `"action":"index"`) {
		t.Errorf("result should have action=index: %s", result)
	}
}

func TestIndexWorktreeTool_Run_confirm(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "my-project", Identity: "my-id",
		Path: "/tmp/my-project", SourceType: "git", Model: "test-model",
	})

	tool := NewIndexWorktreeTool(fs, nil, PolicyConfirm)
	result, err := tool.Run(ctx, `{"project":"my-project"}`)
	if err != nil {
		t.Fatalf("Run(confirm): %v", err)
	}
	if !strings.Contains(result, `"proposed":true`) {
		t.Errorf("result should have proposed=true: %s", result)
	}
	if !strings.Contains(result, `"confirm_required":true`) {
		t.Errorf("result should have confirm_required=true: %s", result)
	}
}

func TestIndexWorktreeTool_Run_noProject(t *testing.T) {
	fs := newFakeSearchStore()
	tool := NewIndexWorktreeTool(fs, nil, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"nonexistent"}`)
	if err != nil {
		t.Fatalf("Run(no project) should not return hard error: %v", err)
	}
	if !strings.Contains(result, `"error"`) {
		t.Errorf("result should contain error message: %s", result)
	}
	if !strings.Contains(result, `"proposed":false`) {
		t.Errorf("result should have proposed=false: %s", result)
	}
}

// TestIndexWorktreeTool_Run_pathOutsideProject verifies the security fix: an
// explicit path outside the registered project's tree is rejected, so an LLM
// cannot point the indexer at an arbitrary filesystem location.
func TestIndexWorktreeTool_Run_pathOutsideProject(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "my-project", Identity: "my-id",
		Path: "/tmp/my-project", SourceType: "git", Model: "test-model",
	})

	tool := NewIndexWorktreeTool(fs, nil, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"my-project","path":"/etc"}`)
	if err != nil {
		t.Fatalf("Run(path outside) should not return hard error: %v", err)
	}
	if !strings.Contains(result, "outside registered project") {
		t.Errorf("result should reject the out-of-tree path: %s", result)
	}
	if !strings.Contains(result, `"proposed":false`) {
		t.Errorf("result should have proposed=false: %s", result)
	}
}

// TestIndexWorktreeTool_Run_pathInsideProject verifies a path that stays inside
// the project's tree (a subdirectory/worktree) is accepted.
func TestIndexWorktreeTool_Run_pathInsideProject(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "my-project", Identity: "my-id",
		Path: "/tmp/my-project", SourceType: "git", Model: "test-model",
	})

	tool := NewIndexWorktreeTool(fs, nil, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"my-project","path":"/tmp/my-project/sub"}`)
	if err != nil {
		t.Fatalf("Run(path inside): %v", err)
	}
	if !strings.Contains(result, `"proposed":true`) {
		t.Errorf("in-tree path should be accepted: %s", result)
	}
	if !strings.Contains(result, "/tmp/my-project/sub") {
		t.Errorf("result should carry the in-tree path: %s", result)
	}
}

func TestParseAndValidateIndexArgs_valid(t *testing.T) {
	args, err := parseAndValidateIndexArgs(`{"project":"p","path":"/tmp","model":"m1"}`)
	if err != nil {
		t.Fatalf("parseAndValidateIndexArgs(valid): %v", err)
	}
	if args.Project != "p" {
		t.Errorf("Project = %q, want %q", args.Project, "p")
	}
	if args.Path != "/tmp" {
		t.Errorf("Path = %q, want %q", args.Path, "/tmp")
	}
	if args.Model != "m1" {
		t.Errorf("Model = %q, want %q", args.Model, "m1")
	}
}

func TestParseAndValidateIndexArgs_invalid(t *testing.T) {
	_, err := parseAndValidateIndexArgs(`{{{broken}}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestParseAndValidateIndexArgs_minimal(t *testing.T) {
	args, err := parseAndValidateIndexArgs(`{"project":"my-project"}`)
	if err != nil {
		t.Fatalf("parseAndValidateIndexArgs(minimal): %v", err)
	}
	if args.Project != "my-project" {
		t.Errorf("Project = %q, want %q", args.Project, "my-project")
	}
	if args.Path != "" {
		t.Errorf("Path = %q, want empty", args.Path)
	}
	if args.Model != "" {
		t.Errorf("Model = %q, want empty", args.Model)
	}
}

// ---------------------------------------------------------------------------
// reindex_project tool
// ---------------------------------------------------------------------------

func TestReindexProjectTool_Def(t *testing.T) {
	tool := NewReindexProjectTool(nil, nil, PolicyPropose)
	def := tool.Def()
	if def.Name != "reindex_project" {
		t.Errorf("Def().Name = %q, want %q", def.Name, "reindex_project")
	}
	if def.Description == "" {
		t.Error("Def().Description is empty")
	}
}

func TestReindexProjectTool_Run_invalidJSON(t *testing.T) {
	tool := NewReindexProjectTool(nil, nil, PolicyPropose)
	_, err := tool.Run(ctx, `{{{bad}}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestReindexProjectTool_Run_propose(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "my-project", Identity: "my-id",
		Path: "/tmp/my-project", SourceType: "git", Model: "test-model",
	})

	tool := NewReindexProjectTool(fs, nil, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"my-project"}`)
	if err != nil {
		t.Fatalf("Run(propose): %v", err)
	}
	if !strings.Contains(result, `"proposed":true`) {
		t.Errorf("result should have proposed=true: %s", result)
	}
	if !strings.Contains(result, `"action":"reindex"`) {
		t.Errorf("result should have action=reindex: %s", result)
	}
}

func TestReindexProjectTool_Run_projectNotFound(t *testing.T) {
	fs := newFakeSearchStore()
	tool := NewReindexProjectTool(fs, nil, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"nonexistent"}`)
	if err != nil {
		t.Fatalf("Run(not found) should not return hard error: %v", err)
	}
	if !strings.Contains(result, `"error"`) {
		t.Errorf("result should contain error message: %s", result)
	}
}

func TestReindexProjectTool_Run_remoteOnly(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{
		Name: "remote-proj", Identity: "remote-id",
		Path: "", SourceType: "git", // empty path = remote-only
	})

	tool := NewReindexProjectTool(fs, nil, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"remote-proj"}`)
	if err != nil {
		t.Fatalf("Run(remote only) should not return hard error: %v", err)
	}
	if !strings.Contains(result, `"error"`) {
		t.Errorf("result should contain error for remote-only: %s", result)
	}
}

// ---------------------------------------------------------------------------
// server_repo_sync tool
// ---------------------------------------------------------------------------

func TestServerRepoSyncTool_Def(t *testing.T) {
	tool := NewServerRepoSyncTool(nil, PolicyPropose)
	def := tool.Def()
	if def.Name != "server_repo_sync" {
		t.Errorf("Def().Name = %q, want %q", def.Name, "server_repo_sync")
	}
	if def.Description == "" {
		t.Error("Def().Description is empty")
	}
}

func TestServerRepoSyncTool_Run_nilClient(t *testing.T) {
	tool := NewServerRepoSyncTool(nil, PolicyPropose)
	result, err := tool.Run(ctx, `{"project":"my-project"}`)
	if err != nil {
		t.Fatalf("Run(nil client) should not return hard error: %v", err)
	}
	if !strings.Contains(result, `"error"`) {
		t.Errorf("result should contain error for nil client: %s", result)
	}
}

func TestServerRepoSyncTool_Run_invalidJSON(t *testing.T) {
	// Need a non-nil client so JSON parsing runs before the client.GetProject
	// HTTP call. The invalid JSON error fires before the HTTP call starts.
	fakeClient := semidxclient.New("http://localhost:1", "test-token")
	tool := NewServerRepoSyncTool(fakeClient, PolicyPropose)
	_, err := tool.Run(ctx, `{{{bad}}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("error should mention 'invalid arguments', got: %v", err)
	}
}
