package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/search"
)

// basisEmbedder maps a chunk to a fixed 3-D basis vector by keyword, so vector
// search has a deterministic, assertable ranking (mirrors the indexing test's fake).
type basisEmbedder struct{}

func (basisEmbedder) basis(text string) []float32 {
	switch {
	case strings.Contains(text, "alpha"):
		return []float32{1, 0, 0}
	case strings.Contains(text, "beta"):
		return []float32{0, 1, 0}
	default:
		return []float32{0, 0, 1}
	}
}
func (basisEmbedder) ModelInfo(_ context.Context, model string) (*embed.ModelInfo, error) {
	return &embed.ModelInfo{Name: model, Dims: 3}, nil
}
func (e basisEmbedder) Embed(_ context.Context, _ string, inputs ...string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		out[i] = e.basis(in)
	}
	return out, nil
}
func (e basisEmbedder) EmbedSingle(_ context.Context, _, text string) ([]float32, error) {
	return e.basis(text), nil
}
func (basisEmbedder) ListModels(_ context.Context) ([]string, error) { return []string{"m"}, nil }

// connectLocal indexes a fixture into a real SQLite store and wires an in-memory
// MCP client to a server backed by the standalone local backend.
func connectLocal(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	writeFile(t, src, "alpha.go", "package a\nfunc Alpha() {} // token alpha here\n")
	writeFile(t, src, "beta.go", "package b\nfunc Beta() {} // token beta here\n")

	pid, err := st.UpsertProject(ctx, "proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	emb := basisEmbedder{}
	if _, err := indexing.NewIndexer(st, emb, 3, indexing.IndexerOpts{Workers: 2, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32}).IndexProject(ctx, pid, src, "m", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
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
	return sess
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLocalBackendSearchOverRealIndex proves `semidx mcp` works standalone: an
// agent's semantic_search against a local SQLite index returns the indexed hit,
// with no server involved.
func TestLocalBackendSearchOverRealIndex(t *testing.T) {
	sess := connectLocal(t)

	text, isErr := callText(t, sess, "semantic_search", map[string]any{"project": "proj", "query": "alpha", "format": "text"})
	if isErr {
		t.Fatalf("unexpected isError; text=%q", text)
	}
	if !strings.Contains(text, "alpha.go:1") {
		t.Errorf("local search missing alpha.go hit: %q", text)
	}
}

func TestLocalBackendProjects(t *testing.T) {
	sess := connectLocal(t)
	text, isErr := callText(t, sess, "semantic_projects", map[string]any{})
	if isErr || !strings.Contains(text, "proj") {
		t.Errorf("local projects text = %q (isErr=%v)", text, isErr)
	}
}

func TestLocalBackendStatus(t *testing.T) {
	sess := connectLocal(t)
	text, isErr := callText(t, sess, "semantic_status", map[string]any{"project": "proj"})
	if isErr || !strings.Contains(text, "Total indexed:") || !strings.Contains(text, "proj") {
		t.Errorf("local status text = %q (isErr=%v)", text, isErr)
	}
}

// TestLocalBackendReindexDegradesGracefully: reindex is a CLI operation locally,
// so the tool returns a clear in-band error pointing at `semidx index`.
func TestLocalBackendReindexDegradesGracefully(t *testing.T) {
	sess := connectLocal(t)
	text, isErr := callText(t, sess, "semantic_reindex", map[string]any{"project": "proj"})
	if !isErr {
		t.Errorf("local reindex should be an in-band error; text=%q", text)
	}
	if !strings.Contains(text, "semidx index") {
		t.Errorf("reindex error should point to the CLI; got %q", text)
	}
}

func TestLocalBackendListToolsExposesAll(t *testing.T) {
	sess := connectLocal(t)
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}
	for _, want := range []string{
		"semantic_search", "semantic_projects", "semantic_reindex", "semantic_status",
		"repo_worktrees", "repo_branches", "repo_status", "semantic_search_multi",
	} {
		if !got[want] {
			t.Errorf("missing tool %q (have %v)", want, got)
		}
	}
}
