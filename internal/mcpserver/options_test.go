package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectServer wires an in-memory MCP session for an already-built server.
func connectServer(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := cli.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// connectWithOptions builds a server via NewWithOptions and connects to it.
func connectWithOptions(t *testing.T, b Backend, o Options) *mcp.ClientSession {
	t.Helper()
	server, err := NewWithOptions(b, o)
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	return connectServer(t, server)
}

// listToolSet returns the set of tool names the session's server exposes.
func listToolSet(t *testing.T, sess *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	return got
}

func TestAllowlistSubsetExposesOnlyRequested(t *testing.T) {
	// gitStub has every capability except ask, so absences prove the
	// allowlist filtered, not a missing capability.
	sess := connectWithOptions(t, &gitStub{}, Options{
		AllowedTools: []string{"semantic_search", "semantic_status"},
	})
	got := listToolSet(t, sess)
	if len(got) != 2 || !got["semantic_search"] || !got["semantic_status"] {
		t.Errorf("tool set = %v, want exactly {semantic_search, semantic_status}", got)
	}
}

func TestAllowlistUnknownToolFailsListingValidNames(t *testing.T) {
	_, err := NewWithOptions(&stubBackend{}, Options{AllowedTools: []string{"semantic_serch"}})
	if err == nil {
		t.Fatal("expected error for unknown tool name")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"semantic_serch"`) {
		t.Errorf("error should name the offender: %q", msg)
	}
	// The message must list every valid name, sourced from the canonical slice.
	for _, name := range toolNames {
		if !strings.Contains(msg, name) {
			t.Errorf("error missing valid tool %q: %q", name, msg)
		}
	}
}

func TestAllowlistAskWithoutAskBackendSkipsWithoutError(t *testing.T) {
	sess := connectWithOptions(t, &stubBackend{}, Options{
		AllowedTools: []string{"semantic_search", "semantic_ask"},
	})
	got := listToolSet(t, sess)
	if got["semantic_ask"] {
		t.Error("semantic_ask must not register without an AskBackend")
	}
	if !got["semantic_search"] {
		t.Errorf("semantic_search should survive the intersection; got %v", got)
	}
}

func TestAllowlistAskWithAskBackendRegisters(t *testing.T) {
	sess := connectWithOptions(t, &fakeAskBackend{}, Options{
		AllowedTools: []string{"semantic_ask"},
	})
	got := listToolSet(t, sess)
	if len(got) != 1 || !got["semantic_ask"] {
		t.Errorf("tool set = %v, want exactly {semantic_ask}", got)
	}
}

func TestAllowlistRepoToolsWithoutGitBackendSkipsWithoutError(t *testing.T) {
	sess := connectWithOptions(t, &stubBackend{}, Options{
		AllowedTools: []string{"repo_worktrees", "repo_branches", "repo_status", "semantic_search_multi", "semantic_projects"},
	})
	got := listToolSet(t, sess)
	for _, name := range []string{"repo_worktrees", "repo_branches", "repo_status", "semantic_search_multi"} {
		if got[name] {
			t.Errorf("%s must not register without the capability", name)
		}
	}
	if len(got) != 1 || !got["semantic_projects"] {
		t.Errorf("tool set = %v, want exactly {semantic_projects}", got)
	}
}

func TestAllowlistCapabilityGatedSubsetRegisters(t *testing.T) {
	sess := connectWithOptions(t, &gitStub{}, Options{
		AllowedTools: []string{"repo_worktrees", "semantic_search_multi"},
	})
	got := listToolSet(t, sess)
	if len(got) != 2 || !got["repo_worktrees"] || !got["semantic_search_multi"] {
		t.Errorf("tool set = %v, want exactly {repo_worktrees, semantic_search_multi}", got)
	}
}

func TestAllowlistBlankEntriesMeanAll(t *testing.T) {
	sess := connectWithOptions(t, &gitStub{}, Options{AllowedTools: []string{"", "  "}})
	got := listToolSet(t, sess)
	want := []string{
		"semantic_search", "semantic_projects", "semantic_reindex", "semantic_status",
		"repo_worktrees", "repo_branches", "repo_status", "semantic_search_multi",
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("blank-only allowlist should mean all tools; missing %q (have %v)", name, got)
		}
	}
}

// TestNewMatchesEmptyOptions locks the compat contract: New(b) exposes exactly
// what NewWithOptions(b, Options{}) exposes.
func TestNewMatchesEmptyOptions(t *testing.T) {
	b := &gitStub{}
	viaNew := listToolSet(t, connectServer(t, New(b)))
	viaOptions := listToolSet(t, connectWithOptions(t, b, Options{}))
	if len(viaNew) != len(viaOptions) {
		t.Fatalf("New = %v, NewWithOptions(Options{}) = %v", viaNew, viaOptions)
	}
	for name := range viaNew {
		if !viaOptions[name] {
			t.Errorf("tool %q registered by New but not by empty Options", name)
		}
	}
}

func TestToolNamesReturnsACopy(t *testing.T) {
	got := ToolNames()
	if len(got) != len(toolNames) {
		t.Fatalf("ToolNames() = %v, want %v", got, toolNames)
	}
	got[0] = "mutated"
	if toolNames[0] == "mutated" {
		t.Error("ToolNames must return a copy, not the canonical slice")
	}
}

func TestRunWithOptionsRejectsInvalidAllowlist(t *testing.T) {
	err := RunWithOptions(context.Background(), &stubBackend{}, Options{AllowedTools: []string{"nope"}})
	if err == nil || !strings.Contains(err.Error(), "unknown MCP tool") {
		t.Fatalf("RunWithOptions err = %v, want unknown-tool error", err)
	}
}
