package main

import (
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/mcpserver"
)

func TestMCPToolAllowlist(t *testing.T) {
	cfg := &config.Config{MCPTools: []string{"semantic_search"}}

	// Flag passed → flag wins, even over a configured env allowlist.
	if got := mcpToolAllowlist(true, []string{"semantic_status"}, cfg); len(got) != 1 || got[0] != "semantic_status" {
		t.Errorf("flag-set allowlist = %v, want [semantic_status]", got)
	}
	// Flag passed empty → explicit reset to "all", ignoring the env.
	if got := mcpToolAllowlist(true, nil, cfg); got != nil {
		t.Errorf("explicit empty flag = %v, want nil (all tools)", got)
	}
	// No flag → SEMIDX_MCP_TOOLS from the layered config.
	if got := mcpToolAllowlist(false, nil, cfg); len(got) != 1 || got[0] != "semantic_search" {
		t.Errorf("env fallback allowlist = %v, want [semantic_search]", got)
	}
	// No flag, no config → nil (all tools).
	if got := mcpToolAllowlist(false, nil, nil); got != nil {
		t.Errorf("no flag/no config = %v, want nil", got)
	}
}

func TestMCPToolsFlagParsesCommaAndRepetition(t *testing.T) {
	cmd := newMCPCmd(&deps{})
	if err := cmd.ParseFlags([]string{"--tools", "semantic_search,semantic_status", "--tools", "repo_status"}); err != nil {
		t.Fatal(err)
	}
	if !cmd.Flags().Changed("tools") {
		t.Error("--tools should report Changed after being passed")
	}
	got, err := cmd.Flags().GetStringSlice("tools")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"semantic_search", "semantic_status", "repo_status"}
	if len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tools[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestMCPCmdHelpListsValidToolNames keeps the command help in sync with the
// canonical tool registry — a new tool must show up here automatically.
func TestMCPCmdHelpListsValidToolNames(t *testing.T) {
	long := newMCPCmd(&deps{}).Long
	for _, name := range mcpserver.ToolNames() {
		if !strings.Contains(long, name) {
			t.Errorf("mcp help missing tool name %q", name)
		}
	}
	if !strings.Contains(long, "SEMIDX_MCP_TOOLS") {
		t.Error("mcp help should document the SEMIDX_MCP_TOOLS env var")
	}
}
