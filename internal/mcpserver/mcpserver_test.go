package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/pkg/client"
)

// stubServer mimics the parts of the semidx HTTP API the MCP tools call.
func stubServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{project}/search", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("project") == "ghost" {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "project not found: ghost"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project": r.PathValue("project"), "model": "bge-m3", "fallback": false,
			"results": []map[string]any{
				{"path": "internal/auth/token.go", "start_line": 42, "score": 0.91, "content": "func Verify() {}"},
			},
		})
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]any{
			{"name": "app", "model": "bge-m3", "status": "ready", "source_type": "git", "git_url": "https://x/y.git"},
		}})
	})
	mux.HandleFunc("POST /api/v1/projects/{project}/index-jobs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": 5, "status": "queued"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// connect wires an in-memory MCP client to a server backed by the stub HTTP API.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	http := stubServer(t)
	server := New(client.New(http.URL, "tok"))

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

func callText(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestListToolsExposesThreeTools(t *testing.T) {
	sess := connect(t)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{"semantic_search", "semantic_projects", "semantic_reindex"} {
		if !got[want] {
			t.Errorf("missing tool %q (have %v)", want, got)
		}
	}
	if got["semantic_index"] {
		t.Error("semantic_index must not exist — arbitrary-path indexing was removed")
	}
}

func TestSemanticSearch(t *testing.T) {
	sess := connect(t)
	text, isErr := callText(t, sess, "semantic_search", map[string]any{"project": "app", "query": "verify token"})
	if isErr {
		t.Fatalf("unexpected isError; text=%q", text)
	}
	if !strings.Contains(text, "internal/auth/token.go:42") || !strings.Contains(text, "0.910") {
		t.Errorf("search text missing file:line/score: %q", text)
	}
}

func TestSemanticSearchProjectNotFoundIsInBandError(t *testing.T) {
	sess := connect(t)
	text, isErr := callText(t, sess, "semantic_search", map[string]any{"project": "ghost", "query": "x"})
	if !isErr {
		t.Errorf("expected isError=true for missing project; text=%q", text)
	}
	if !strings.Contains(text, "not found") {
		t.Errorf("error text = %q", text)
	}
}

func TestSemanticProjects(t *testing.T) {
	sess := connect(t)
	text, isErr := callText(t, sess, "semantic_projects", map[string]any{})
	if isErr || !strings.Contains(text, "app") || !strings.Contains(text, "status=ready") {
		t.Errorf("projects text = %q (isErr=%v)", text, isErr)
	}
}

func TestSemanticReindex(t *testing.T) {
	sess := connect(t)
	text, isErr := callText(t, sess, "semantic_reindex", map[string]any{"project": "app"})
	if isErr || !strings.Contains(text, "#5") || !strings.Contains(text, "full") {
		t.Errorf("reindex text = %q (isErr=%v)", text, isErr)
	}
}
