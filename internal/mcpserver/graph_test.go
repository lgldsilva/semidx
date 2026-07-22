package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestGraphTools exercises the graph primitives (neighbors, trace, symbols)
// over a real SQLite-backed local MCP session. Setup lives in setupLocalMCP
// (shared with the local-backend search tests); the four assertions are split
// into helpers so this test's body stays flat and well under the cognitive
// complexity gate.
func TestGraphTools(t *testing.T) {
	fix := setupLocalMCP(t, map[string]string{
		"main.go": "package main\nimport \"fmt\"\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n",
		"lib.go":  "package main\nfunc Helper() {}\n",
	})

	// Insert dependencies after indexing so they are not wiped out.
	if err := fix.st.InsertFileDependencies(fix.ctx, fix.pid, "main.go", []string{"lib.go"}); err != nil {
		t.Fatalf("InsertFileDependencies: %v", err)
	}

	t.Run("semantic_neighbors", func(t *testing.T) { assertGraphNeighbors(t, fix.sess) })
	t.Run("semantic_trace", func(t *testing.T) { assertGraphTrace(t, fix.sess) })
	t.Run("semantic_symbols", func(t *testing.T) { assertGraphSymbols(t, fix.sess) })
	t.Run("semantic_symbols path safety", func(t *testing.T) { assertGraphSymbolsPathSafety(t, fix.sess) })
}

// assertGraphNeighbors checks the neighbors tool returns lib.go as an import of
// main.go.
func assertGraphNeighbors(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	text, isErr := callText(t, sess, "semantic_neighbors", map[string]any{
		"project": "proj",
		"file":    "main.go",
	})
	if isErr {
		t.Fatalf("neighbors error: %s", text)
	}
	var res map[string][]string
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("failed to unmarshal neighbors: %v", err)
	}
	if len(res["imports"]) != 1 || res["imports"][0] != "lib.go" {
		t.Errorf("unexpected neighbors: %v", res)
	}
}

// assertGraphTrace checks the trace tool reaches lib.go from main.go at depth 1.
func assertGraphTrace(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	text, isErr := callText(t, sess, "semantic_trace", map[string]any{
		"project": "proj",
		"files":   []string{"main.go"},
	})
	if isErr {
		t.Fatalf("trace error: %s", text)
	}
	var res map[string]int
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("failed to unmarshal trace: %v", err)
	}
	if res["lib.go"] != 1 {
		t.Errorf("unexpected trace: %v", res)
	}
}

// assertGraphSymbols checks the symbols tool extracts the main function from
// main.go.
func assertGraphSymbols(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	text, isErr := callText(t, sess, "semantic_symbols", map[string]any{
		"project": "proj",
		"file":    "main.go",
	})
	if isErr {
		t.Fatalf("symbols error: %s", text)
	}
	if !strings.Contains(text, "main") {
		t.Errorf("expected symbols to contain 'main', got: %s", text)
	}
}

// assertGraphSymbolsPathSafety checks the symbols tool rejects paths that try
// to escape the project root.
func assertGraphSymbolsPathSafety(t *testing.T, sess *mcp.ClientSession) {
	t.Helper()
	text, isErr := callText(t, sess, "semantic_symbols", map[string]any{
		"project": "proj",
		"file":    "../outside.go",
	})
	if !isErr {
		t.Fatalf("expected error for unsafe path, got: %s", text)
	}
	if !strings.Contains(text, "parent directory segments") {
		t.Errorf("expected unsafe path error message, got: %s", text)
	}
}
