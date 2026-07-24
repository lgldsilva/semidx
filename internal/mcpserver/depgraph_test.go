package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/pkg/client"
)

// TestDepGraphTools exercises semantic_subgraph and semantic_path over a real
// SQLite-backed local MCP session, so the walk sees the same adjacency map and
// file inventory the store actually returns.
func TestDepGraphTools(t *testing.T) {
	fix := setupLocalMCP(t, map[string]string{
		"main.go":     "package main\nfunc main() {}\n",
		"pkg/util.go": "package pkg\nfunc Help() {}\n",
	})
	// Insert the import edge after indexing so it is not wiped out. The store
	// records file → package-dir, which is what the walkable index expects.
	if err := fix.st.InsertFileDependencies(fix.ctx, fix.pid, "main.go", []string{"pkg/"}); err != nil {
		t.Fatalf("InsertFileDependencies: %v", err)
	}

	t.Run("subgraph", func(t *testing.T) { assertDepSubgraph(t, fix.sess) })
	t.Run("path", func(t *testing.T) { assertDepPath(t, fix.sess) })
	t.Run("path requires from and to", func(t *testing.T) { assertDepPathRequiresEnds(t, fix.sess) })
	t.Run("subgraph rejects escaping seed", func(t *testing.T) { assertDepSubgraphPathSafety(t, fix.sess) })
}

func assertDepSubgraph(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	text, isErr := callText(t, sess, "semantic_subgraph", map[string]any{
		"project": "proj",
		"file":    "main.go",
	})
	if isErr {
		t.Fatalf("subgraph error: %s", text)
	}
	var res client.GraphSubgraphResponse
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("unmarshal subgraph: %v\n%s", err, text)
	}
	var sawImport bool
	for _, e := range res.Edges {
		if e.Source == "main.go" && e.Target == "pkg/" && e.Kind == "imports" {
			sawImport = true
		}
	}
	if !sawImport {
		t.Errorf("subgraph missing main.go -[imports]-> pkg/: %+v", res.Edges)
	}
	// The seed node must be flagged so a client can centre its rendering on it.
	for _, n := range res.Nodes {
		if n.ID == "main.go" && !n.Seed {
			t.Error("seed node main.go is not flagged seed=true")
		}
	}
}

func assertDepPath(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	text, isErr := callText(t, sess, "semantic_path", map[string]any{
		"project": "proj",
		"from":    "main.go",
		"to":      "pkg/util.go",
	})
	if isErr {
		t.Fatalf("path error: %s", text)
	}
	var res client.GraphPathResponse
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("unmarshal path: %v\n%s", err, text)
	}
	if !res.Found || !res.Directed {
		t.Fatalf("path not found directed: %+v", res)
	}
	// The hop through the package dir is what makes two files connectable.
	if want := []string{"main.go", "pkg/", "pkg/util.go"}; strings.Join(res.Hops, ",") != strings.Join(want, ",") {
		t.Errorf("hops = %v; want %v", res.Hops, want)
	}
}

func assertDepPathRequiresEnds(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	text, isErr := callText(t, sess, "semantic_path", map[string]any{
		"project": "proj",
		"from":    "main.go",
		"to":      "",
	})
	if !isErr {
		t.Fatalf("expected an error with an empty to; got %s", text)
	}
	if !strings.Contains(text, "from and to are required") {
		t.Errorf("unexpected error text: %s", text)
	}
}

func assertDepSubgraphPathSafety(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	text, isErr := callText(t, sess, "semantic_subgraph", map[string]any{
		"project": "proj",
		"file":    "../outside.go",
	})
	if !isErr {
		t.Fatalf("expected an error for an escaping seed; got %s", text)
	}
	if !strings.Contains(text, "parent directory segments") {
		t.Errorf("unexpected error text: %s", text)
	}
}

// TestDepGraphToolsAreAllowlistable guards the allowlist wiring: the two names
// must be known to resolveAllowedTools, else --tools would reject them.
func TestDepGraphToolsAreAllowlistable(t *testing.T) {
	allowed, explicit, err := resolveAllowedTools([]string{toolSemanticSubgraph, toolSemanticPath})
	if err != nil {
		t.Fatalf("resolveAllowedTools: %v", err)
	}
	if !explicit || !allowed[toolSemanticSubgraph] || !allowed[toolSemanticPath] {
		t.Fatalf("allowed=%v explicit=%v", allowed, explicit)
	}
}
