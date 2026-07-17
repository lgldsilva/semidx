package webadmin

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/store"
)

type chunkByDimsStore struct {
	*fakeStore
	byDims map[int][]store.SearchResult
}

func (s *chunkByDimsStore) FetchChunksByPath(_ context.Context, _ int, _ string, dims int, _ int) ([]store.SearchResult, error) {
	if ch, ok := s.byDims[dims]; ok {
		return ch, nil
	}
	return nil, nil
}

func TestFetchProjectFileChunksFallbackDims(t *testing.T) {
	fs := &chunkByDimsStore{
		fakeStore: newFakeStore(),
		byDims: map[int][]store.SearchResult{
			768: {{Content: "fallback", StartLine: 1, EndLine: 1}},
		},
	}
	chunks, dims, err := fetchProjectFileChunks(context.Background(), fs, 1, "a.go", 1024)
	if err != nil || dims != 768 || len(chunks) != 1 {
		t.Fatalf("chunks=%v dims=%d err=%v", chunks, dims, err)
	}
}

func TestFetchExplainChunksFallback(t *testing.T) {
	fs := &chunkByDimsStore{
		fakeStore: newFakeStore(),
		byDims: map[int][]store.SearchResult{
			768: {{Content: "explain", StartLine: 2, EndLine: 3}},
		},
	}
	proj := &store.Project{ID: 1, Dims: 1024}
	chunks, err := fetchExplainChunks(context.Background(), fs, proj, "main.go")
	if err != nil || len(chunks) != 1 || chunks[0].Content != "explain" {
		t.Fatalf("chunks=%v err=%v", chunks, err)
	}
}

func TestImportersOfFile(t *testing.T) {
	fs := newFakeStore()
	fs.graph = map[string][]string{
		"cmd/main.go": {"internal/auth/"},
		"other.go":    {"internal/util/"},
	}
	got := importersOfFile(context.Background(), fs, 1, "internal/auth/token.go")
	if len(got) != 1 || got[0] != "cmd/main.go" {
		t.Fatalf("importers=%v", got)
	}
}

func TestDetectModulePathAndFindTestFiles(t *testing.T) {
	root := t.TempDir()
	gm := filepath.Join(root, "go.mod")
	if err := os.WriteFile(gm, []byte("module example.com/demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectModulePath(root); got != "example.com/demo" {
		t.Fatalf("module=%q", got)
	}

	srcDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "foo.go"), []byte("package pkg\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "foo_test.go"), []byte("package pkg\nfunc TestFoo(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := findTestFiles(root, "pkg/foo.go", "Foo")
	if len(tests) != 1 || !strings.HasSuffix(tests[0], "foo_test.go") {
		t.Fatalf("tests=%v", tests)
	}
}

func TestRevokeControlTokenAPI(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	_, body := postAdminJSON(t, c, srv.URL+"/admin/api/tokens", csrf, map[string]any{
		"name": "ctl", "scopes": []string{"read"}, "ttl_days": 1,
	})
	if !strings.Contains(body, `"id":`) {
		t.Fatalf("create token body=%s", body)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/tokens/1", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("revoke token = %d", resp.StatusCode)
	}
}

func TestAnalyzeDeadCodeWithCheckout(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Path: root}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/dead-code?limit=10")
	if code != 200 || !strings.Contains(body, `"findings"`) {
		t.Fatalf("dead-code = %d body=%s", code, body)
	}
}

func TestAnalyzeCallersWithGraph(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	fs.graph = map[string][]string{"main.go": {"internal/auth/"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/callers?path=internal/auth/token.go")
	if code != 200 || !strings.Contains(body, `"main.go"`) {
		t.Fatalf("callers graph = %d body=%s", code, body)
	}
}

func TestSettingsPasswordValidation(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/account/password", csrf, map[string]any{
		"current": "wrong", "new": "newpassword1",
	})
	if code != 400 || !strings.Contains(body, "incorrect") {
		t.Fatalf("bad current = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/account/password", csrf, map[string]any{
		"current": "supersecret", "new": "short",
	})
	if code != 400 || !strings.Contains(body, "8 characters") {
		t.Fatalf("short password = %d body=%s", code, body)
	}
}

// coverage-patch: 2026-07-17
func TestAPIKeysCreateAndRevoke(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	// apiCreateKey — bad JSON
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/keys", csrf, "not-json")
	if code != 400 || !strings.Contains(body, "invalid JSON") {
		t.Fatalf("bad json = %d body=%s", code, body)
	}

	// apiCreateKey — empty name
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/keys", csrf, map[string]any{
		"name": "", "scopes": []string{"read"},
	})
	if code != 400 || !strings.Contains(body, "name is required") {
		t.Fatalf("empty name = %d body=%s", code, body)
	}

	// apiCreateKey — invalid scope
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/keys", csrf, map[string]any{
		"name": "badscope", "scopes": []string{"bogus"},
	})
	if code != 400 || !strings.Contains(body, "invalid scope") {
		t.Fatalf("bad scope = %d body=%s", code, body)
	}

	// apiCreateKey — happy path
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/keys", csrf, map[string]any{
		"name": "mykey", "scopes": []string{"read"},
	})
	if code != 201 || !strings.Contains(body, `"id":`) || !strings.Contains(body, "mykey") {
		t.Fatalf("create key = %d body=%s", code, body)
	}

	// apiRevokeKey — invalid id (non-numeric)
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/keys/abc", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("invalid key id = %d", resp.StatusCode)
	}

	// apiRevokeKey — not found
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/keys/999", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("key not found = %d", resp.StatusCode)
	}

	// apiRevokeKey — happy path (revoke key 1 created above)
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/keys/1", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("revoke key = %d", resp.StatusCode)
	}
}
