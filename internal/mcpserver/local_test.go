package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// localFixture is the shared setup result for tests that need a real SQLite
// store wired to an in-memory MCP session through the standalone local backend.
// pid and st let callers add fixtures (e.g. graph edges) after indexing; src is
// the temp source root in case a test needs to write more files.
type localFixture struct {
	ctx  context.Context
	st   *localstore.SQLiteStore
	pid  int
	sess *mcp.ClientSession
	src  string
}

// setupLocalMCP indexes a fixture into a real SQLite store and wires an
// in-memory MCP client to a server backed by the standalone local backend.
// The returned fixture owns the store, session and temp source root; cleanups
// are registered with t so callers can return early on failure.
func setupLocalMCP(t *testing.T, files map[string]string) localFixture {
	t.Helper()
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	for name, content := range files {
		writeFile(t, src, name, content)
	}

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
	return localFixture{ctx: ctx, st: st, pid: pid, sess: sess, src: src}
}

// connectLocal is a thin wrapper over setupLocalMCP for the common two-file
// alpha/beta fixture used by the local-backend search tests. It keeps the
// pre-existing *mcp.ClientSession return type so existing callers compile.
func connectLocal(t *testing.T) *mcp.ClientSession {
	t.Helper()
	return setupLocalMCP(t, map[string]string{
		"alpha.go": "package a\nfunc Alpha() {} // token alpha here\n",
		"beta.go":  "package b\nfunc Beta() {} // token beta here\n",
	}).sess
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	// Nested fixture paths (e.g. "pkg/util.go") are what the dependency-graph
	// tests need to get a real package hop, so create the parents.
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
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
	if !strings.Contains(text, "alpha.go:") {
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

// degradedEmbedder indexes normally (basis vectors) but fails query embedding
// with an open circuit, so a search must degrade to keyword results.
type degradedEmbedder struct{ basisEmbedder }

func (degradedEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, &embed.RetryableError{Err: errors.New("circuit open"), After: 2 * time.Second}
}

// TestLocalBackendSearchDegradesWhenCircuitOpen: the local backend must copy
// the search service's Degraded/RetryAfter flags into the MCP SearchOutput.
func TestLocalBackendSearchDegradesWhenCircuitOpen(t *testing.T) {
	ctx := context.Background()
	st, err := localstore.New(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	writeFile(t, src, "alpha.go", "package a\nfunc Alpha() {} // token alpha here\n")
	pid, err := st.UpsertProject(ctx, "proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := indexing.NewIndexer(st, basisEmbedder{}, 3, indexing.IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32}).IndexProject(ctx, pid, src, "m", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	b := NewLocalBackend(search.NewService(st, degradedEmbedder{}), st, false, agent.Capabilities{})
	out, err := b.Search(ctx, "proj", "where is the alpha token", "", 5, false, 0)
	if err != nil {
		t.Fatalf("degraded search must not error: %v", err)
	}
	if !out.Degraded || !out.Fallback {
		t.Errorf("degraded=%v fallback=%v, want both true", out.Degraded, out.Fallback)
	}
	if out.RetryAfterMS != 2000 {
		t.Errorf("RetryAfterMS = %d, want 2000", out.RetryAfterMS)
	}
	if len(out.Results) == 0 {
		t.Error("degraded search should still return keyword results")
	}
}
