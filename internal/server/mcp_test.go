package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lgldsilva/semidx/internal/agent"
	"github.com/lgldsilva/semidx/internal/store"
)

// errBoom is a generic backend failure whose message must never reach agents.
var errBoom = errors.New("dsn=postgres://secret internal boom")

// bearerTransport injects an Authorization: Bearer header on every request.
type bearerTransport struct{ token string }

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// mcpTestServer starts an httptest server with the MCP HTTP endpoint enabled.
func mcpTestServer(t *testing.T, st store.Store) *httptest.Server {
	t.Helper()
	srv := New(st, fakeEmbedder{}, nil)
	srv.EnableMCPHTTP()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// mcpConnect opens a real MCP session against the test server's /mcp endpoint
// over Streamable HTTP, authenticating with the given bearer token.
func mcpConnect(t *testing.T, ts *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token}},
		DisableStandaloneSSE: true,
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := cli.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("MCP connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// callTool invokes a tool and returns its first text content and IsError.
func callTool(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text = tc.Text
			break
		}
	}
	return text, res.IsError
}

func TestMCPHTTPRouteAbsentByDefault(t *testing.T) {
	srv := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}}, fakeEmbedder{}, nil)
	if rec := do(t, srv, "POST", "/mcp", "tok", "{}"); rec.Code != 404 {
		t.Errorf("/mcp without EnableMCPHTTP = %d, want 404", rec.Code)
	}
}

func TestMCPHTTPRequiresBearer(t *testing.T) {
	srv := New(&fakeStore{token: &store.Token{Scopes: []string{"read"}}}, fakeEmbedder{}, nil)
	srv.EnableMCPHTTP()
	if rec := do(t, srv, "POST", "/mcp", "", "{}"); rec.Code != 401 {
		t.Errorf("/mcp without token = %d, want 401", rec.Code)
	}
	// Invalid token (store lookup returns nil) is also rejected.
	noTok := New(&fakeStore{token: nil}, fakeEmbedder{}, nil)
	noTok.EnableMCPHTTP()
	if rec := do(t, noTok, "POST", "/mcp", "bad", "{}"); rec.Code != 401 {
		t.Errorf("/mcp with invalid token = %d, want 401", rec.Code)
	}
}

func TestMCPHTTPInitializeAndSearch(t *testing.T) {
	ts := mcpTestServer(t, &fakeStore{
		token:   &store.Token{ID: 1, Scopes: []string{"read"}},
		project: &store.Project{ID: 1, Name: "proj", Model: "bge-m3"},
		results: []store.SearchResult{{FilePath: "a.go", StartLine: 5, EndLine: 7, Score: 0.9, Content: "x"}},
	})
	sess := mcpConnect(t, ts, "tok")

	if got := sess.InitializeResult().ServerInfo.Name; got != "semidx" {
		t.Errorf("serverInfo.name = %q, want semidx", got)
	}

	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"semantic_search", "semantic_projects", "semantic_reindex", "semantic_status"} {
		if !names[want] {
			t.Errorf("missing tool %q over HTTP; got %v", want, names)
		}
	}

	text, isErr := callTool(t, sess, "semantic_search", map[string]any{"project": "proj", "query": "auth"})
	if isErr {
		t.Fatalf("semantic_search errored: %q", text)
	}
	if !strings.Contains(text, "a.go") {
		t.Errorf("search result should cite a.go; got %q", text)
	}
}

func TestMCPHTTPStatusAndProjects(t *testing.T) {
	ts := mcpTestServer(t, &fakeStore{
		token:     &store.Token{ID: 1, Scopes: []string{"read"}},
		project:   &store.Project{ID: 1, Name: "proj", Model: "bge-m3", SourceType: "git", Status: "ready"},
		listed:    []store.Project{{Name: "proj", SourceType: "git", Status: "ready", Model: "bge-m3"}},
		fileCount: 42,
	})
	sess := mcpConnect(t, ts, "tok")

	text, isErr := callTool(t, sess, "semantic_status", map[string]any{"project": "proj"})
	if isErr || !strings.Contains(text, "Total indexed: 42 files") {
		t.Errorf("semantic_status = %q (isErr=%v), want 42 files", text, isErr)
	}

	text, isErr = callTool(t, sess, "semantic_projects", map[string]any{})
	if isErr || !strings.Contains(text, "proj") {
		t.Errorf("semantic_projects = %q (isErr=%v)", text, isErr)
	}
}

func TestMCPHTTPReindexRequiresWriteScope(t *testing.T) {
	makeStore := func(scopes ...string) *fakeStore {
		return &fakeStore{
			token:      &store.Token{ID: 1, Scopes: scopes},
			project:    &store.Project{ID: 1, Name: "proj", Model: "bge-m3"},
			enqueuedID: 7,
		}
	}

	// read-only token: endpoint reachable, reindex denied in-band.
	sess := mcpConnect(t, mcpTestServer(t, makeStore("read")), "tok")
	text, isErr := callTool(t, sess, "semantic_reindex", map[string]any{"project": "proj"})
	if !isErr {
		t.Fatalf("reindex with read-only token must fail in-band; got %q", text)
	}
	if !strings.Contains(text, "write") {
		t.Errorf("scope error should name the missing scope; got %q", text)
	}

	// write scope: allowed.
	sess = mcpConnect(t, mcpTestServer(t, makeStore("read", "write")), "tok")
	text, isErr = callTool(t, sess, "semantic_reindex", map[string]any{"project": "proj"})
	if isErr {
		t.Fatalf("reindex with write scope errored: %q", text)
	}
	if !strings.Contains(text, "#7") || !strings.Contains(text, "proj") {
		t.Errorf("reindex message = %q, want job #7 for proj", text)
	}

	// admin grants everything, like the REST routes.
	sess = mcpConnect(t, mcpTestServer(t, makeStore("admin")), "tok")
	if text, isErr = callTool(t, sess, "semantic_reindex", map[string]any{"project": "proj"}); isErr {
		t.Errorf("reindex with admin scope errored: %q", text)
	}
}

func TestMCPHTTPReindexValidation(t *testing.T) {
	st := &fakeStore{
		token:   &store.Token{ID: 1, Scopes: []string{"admin"}},
		project: &store.Project{ID: 1, Name: "proj", Model: "bge-m3"},
	}
	sess := mcpConnect(t, mcpTestServer(t, st), "tok")
	text, isErr := callTool(t, sess, "semantic_reindex", map[string]any{"project": "proj", "type": "bogus"})
	if !isErr || !strings.Contains(text, "full") {
		t.Errorf("bad job type should error in-band naming valid types; got %q (isErr=%v)", text, isErr)
	}
}

func TestMCPHTTPProjectNotFoundIsSanitized(t *testing.T) {
	st := &fakeStore{token: &store.Token{ID: 1, Scopes: []string{"read"}}} // project nil → ErrNotFound
	sess := mcpConnect(t, mcpTestServer(t, st), "tok")
	text, isErr := callTool(t, sess, "semantic_status", map[string]any{"project": "ghost"})
	if !isErr || text != "project not found" {
		t.Errorf("missing project = %q (isErr=%v), want sanitized 'project not found'", text, isErr)
	}
}

func TestServerBackendCapabilities(t *testing.T) {
	b := &serverBackend{s: New(&fakeStore{}, fakeEmbedder{}, nil)}
	if b.Capabilities().Flags&agent.CapRemoteIndex == 0 {
		t.Error("server backend must report the remote-index capability")
	}
}

func TestServerBackendErrorsAreSanitized(t *testing.T) {
	ctx := contextWithScopes(context.Background(), []string{"admin"})

	// Generic store errors collapse to op-level messages (no internals).
	boom := &serverBackend{s: New(&fakeStore{getErr: errBoom, listErr: errBoom}, fakeEmbedder{}, nil)}
	if _, err := boom.Search(ctx, "p", "q", "", 5, false, 0); err == nil || err.Error() != "search failed" {
		t.Errorf("Search err = %v, want sanitized 'search failed'", err)
	}
	if _, err := boom.Projects(ctx); err == nil || err.Error() != "list projects failed" {
		t.Errorf("Projects err = %v, want 'list projects failed'", err)
	}
	if _, err := boom.Status(ctx, "p"); err == nil || err.Error() != "status failed" {
		t.Errorf("Status err = %v, want 'status failed'", err)
	}

	// Status: file-count failure after a resolved project.
	countErr := &serverBackend{s: New(&fakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "m"}, fileCountErr: errBoom,
	}, fakeEmbedder{}, nil)}
	if _, err := countErr.Status(ctx, "p"); err == nil || err.Error() != "count files failed" {
		t.Errorf("Status count err = %v, want 'count files failed'", err)
	}

	// Reindex: missing project and enqueue failure.
	missing := &serverBackend{s: New(&fakeStore{}, fakeEmbedder{}, nil)}
	if _, err := missing.Reindex(ctx, "ghost", ""); err == nil || err.Error() != "project not found" {
		t.Errorf("Reindex missing project err = %v, want 'project not found'", err)
	}
	enq := &serverBackend{s: New(&fakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "m"}, enqueueErr: errBoom,
	}, fakeEmbedder{}, nil)}
	if _, err := enq.Reindex(ctx, "p", "full"); err == nil || err.Error() != "enqueue job failed" {
		t.Errorf("Reindex enqueue err = %v, want 'enqueue job failed'", err)
	}
}

func TestServerBackendSearchDefaultsGraphDepth(t *testing.T) {
	b := &serverBackend{s: New(&fakeStore{
		project: &store.Project{ID: 1, Name: "p", Model: "m"},
		results: []store.SearchResult{{FilePath: "a.go", StartLine: 1, EndLine: 2, Score: 1, Content: "x"}},
	}, fakeEmbedder{}, nil)}
	out, err := b.Search(context.Background(), "p", "q", "", 5, false, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].Path != "a.go" {
		t.Errorf("Search results = %+v", out.Results)
	}
}

func TestScopesFromContext(t *testing.T) {
	if got := ScopesFromContext(context.Background()); got != nil {
		t.Errorf("no scopes stored: got %v, want nil", got)
	}
	ctx := contextWithScopes(context.Background(), []string{"read", "write"})
	if got := ScopesFromContext(ctx); len(got) != 2 || got[0] != "read" {
		t.Errorf("ScopesFromContext = %v", got)
	}
	if hasScope(ctx, "admin") {
		t.Error("hasScope(admin) must be false for a read/write token")
	}
	if !hasScope(contextWithScopes(context.Background(), []string{"admin"}), "write") {
		t.Error("admin scope must grant write")
	}
}
