package agent

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/permission"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestApplyActionPolicy_modes(t *testing.T) {
	ctx := context.Background()
	base := func() map[string]any { return map[string]any{"action": "index"} }
	req := permission.Request{Tool: "index_worktree"}

	// Propose never proceeds and marks the result proposed.
	if proceed, resp := applyActionPolicy(ctx, PolicyPropose, nil, req, base()); proceed || !strings.Contains(resp.Content, `"proposed":true`) {
		t.Errorf("propose: proceed=%v resp=%s", proceed, resp.Content)
	}
	// Confirm + deny does not proceed and reports the denial.
	if proceed, resp := applyActionPolicy(ctx, PolicyConfirm, permission.DenyAll, req, base()); proceed || !strings.Contains(resp.Content, `"approved":false`) {
		t.Errorf("confirm-deny: proceed=%v resp=%s", proceed, resp.Content)
	}
	// Confirm + allow proceeds.
	if proceed, _ := applyActionPolicy(ctx, PolicyConfirm, permission.AllowAll, req, base()); !proceed {
		t.Error("confirm-allow must proceed")
	}
	// Confirm without an approver is a soft error, never proceeds.
	if proceed, resp := applyActionPolicy(ctx, PolicyConfirm, nil, req, base()); proceed || !resp.IsError {
		t.Errorf("confirm without approver: proceed=%v isErr=%v", proceed, resp.IsError)
	}
	// Execute proceeds directly.
	if proceed, _ := applyActionPolicy(ctx, PolicyExecute, nil, req, base()); !proceed {
		t.Error("execute must proceed")
	}
}

// coverage-patch: 2026-07-17 — covers the default/unknown policy branch (86.7% → 100%)
func TestApplyActionPolicy_unknown(t *testing.T) {
	ctx := context.Background()
	base := func() map[string]any { return map[string]any{"action": "index"} }
	req := permission.Request{Tool: "index_worktree"}

	proceed, resp := applyActionPolicy(ctx, ActionPolicy(99), nil, req, base())
	if proceed {
		t.Error("unknown policy must not proceed")
	}
	if !resp.IsError {
		t.Error("unknown policy should return a soft error")
	}
	if !strings.Contains(resp.Content, "unknown action policy") {
		t.Errorf("error should mention unknown action policy, got: %s", resp.Content)
	}
}

func TestActionTools_gatedByDeps(t *testing.T) {
	// No indexer and no client → no action tools.
	if got := ActionTools(newFakeSearchStore(), nil, nil, PolicyPropose, nil); len(got) != 0 {
		t.Errorf("no deps → %d tools, want 0", len(got))
	}
}

func TestIndexWorktreeF_proposeAndPathGuard(t *testing.T) {
	fs := newFakeSearchStore()
	fs.addProject(&store.Project{Name: "p", Identity: "id", Path: "/tmp/p", SourceType: "git", Model: "m"})
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 0, indexing.IndexerOpts{Workers: 1})

	tools := ActionTools(fs, idx, nil, PolicyPropose, nil)
	iw := findTool(tools, "index_worktree")
	if iw == nil {
		t.Fatal("index_worktree not registered")
	}
	if iw.Info().Parallel {
		t.Error("action tools must not be parallel")
	}

	// Propose: describes, does not execute.
	resp, err := iw.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p"}`})
	if err != nil {
		t.Fatalf("Run(propose): %v", err)
	}
	if !strings.Contains(resp.Content, `"proposed":true`) {
		t.Errorf("propose result: %s", resp.Content)
	}

	// Security regression: an explicit path outside the project's tree is rejected.
	resp, err = iw.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"p","path":"/etc"}`})
	if err != nil {
		t.Fatalf("Run(out-of-tree): %v", err)
	}
	if !resp.IsError || !strings.Contains(resp.Content, "outside") {
		t.Errorf("out-of-tree path must be rejected: isErr=%v resp=%s", resp.IsError, resp.Content)
	}

	// Unknown project → soft error.
	resp, _ = iw.Run(t.Context(), fantasy.ToolCall{Input: `{"project":"nope"}`})
	if !resp.IsError {
		t.Errorf("unknown project should be a soft error: %s", resp.Content)
	}
}
