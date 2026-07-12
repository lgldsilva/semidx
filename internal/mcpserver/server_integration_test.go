package mcpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/server"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

// TestClientBackendAgainstRealServer is the server-mode integration test: a real
// pgvector store + the real HTTP server (server.Handler over httptest) + a real
// API token, driven through the MCP remote backend and an in-memory MCP session.
// It proves the whole with-server path — auth, handlers, store, client SDK, MCP
// tools — not just a hand-rolled stub. Skips when no Docker provider is present.
func TestClientBackendAgainstRealServer(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()

	ctr, err := postgres.Run(ctx, "pgvector/pgvector:pg16",
		postgres.WithDatabase("semantic_indexer"),
		postgres.WithUsername("semantic"),
		postgres.WithPassword("semantic"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start pgvector: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	st, err := store.NewPgStore(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPgStore: %v", err)
	}
	t.Cleanup(st.Close)

	// Index a fixture into the store the server reads (dims=3, basis embedder).
	emb := basisEmbedder{}
	src := t.TempDir()
	writeFile(t, src, "auth.go", "package a\nfunc Alpha() {} // token alpha here\n")
	if err := st.EnsureChunksTable(ctx, 3); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}
	pid, err := st.UpsertProject(ctx, "proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := indexing.NewIndexer(st, emb, 3, indexing.IndexerOpts{Workers: 2, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32}).IndexProject(ctx, pid, src, "m", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	// Real HTTP server over httptest, reachable with an admin API token.
	const tok = "integration-token"
	if _, err := st.CreateToken(ctx, "integration", server.HashToken(tok), []string{"admin"}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	srv := server.New(st, emb, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// MCP remote backend -> real server, driven over an in-memory MCP session.
	mcpSrv := New(NewClientBackend(client.New(ts.URL, tok)))
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := mcpSrv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := cli.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	// semantic_search flows CLI SDK -> HTTP auth -> handler -> pgvector and back.
	text, isErr := callText(t, sess, "semantic_search", map[string]any{"project": "proj", "query": "alpha"})
	if isErr {
		t.Fatalf("semantic_search via real server errored: %s", text)
	}
	if !strings.Contains(text, "auth.go") {
		t.Errorf("real-server search missing the indexed hit: %q", text)
	}

	// semantic_projects lists the registered project via the real API.
	ptext, isErr := callText(t, sess, "semantic_projects", map[string]any{})
	if isErr || !strings.Contains(ptext, "proj") {
		t.Errorf("semantic_projects via real server = %q (isErr=%v)", ptext, isErr)
	}
}
