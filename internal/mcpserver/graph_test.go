package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/search"
)

func TestGraphTools(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	// Create some files for symbols extraction
	writeFile(t, src, "main.go", "package main\nimport \"fmt\"\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")
	writeFile(t, src, "lib.go", "package main\nfunc Helper() {}\n")

	pid, err := st.UpsertProject(ctx, "proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// Index the project so files are registered
	emb := basisEmbedder{}
	if _, err := indexing.NewIndexer(st, emb, 3, indexing.IndexerOpts{Workers: 2, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32}).IndexProject(ctx, pid, src, "m", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	// Insert dependencies after indexing so they are not wiped out
	err = st.InsertFileDependencies(ctx, pid, "main.go", []string{"lib.go"})
	if err != nil {
		t.Fatalf("InsertFileDependencies: %v", err)
	}

	server := New(NewLocalBackend(search.NewService(st, emb), st, false, agent.Capabilities{Flags: agent.CapLocalGit | agent.CapIndexLocal}))
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := cli.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	t.Run("semantic_neighbors", func(t *testing.T) {
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
	})

	t.Run("semantic_trace", func(t *testing.T) {
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
	})

	t.Run("semantic_symbols", func(t *testing.T) {
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
	})

	t.Run("semantic_symbols path safety", func(t *testing.T) {
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
	})
}
