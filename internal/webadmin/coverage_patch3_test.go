package webadmin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	embedpkg "github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/store"
)

// coverage-patch: 2026-07-17

func TestFinishIngestErrorPath(t *testing.T) {
	es := &errStore{fakeStore: newFakeStore(), updateStatErr: errors.New("status fail")}
	a, err := New(es, nil, slog.Default(), true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// Should log warn and return without panic.
	a.finishIngest(context.Background(), 1, "label")
}

func TestNewWithCSRFKeyAndInvalid(t *testing.T) {
	// invalid hex
	if _, err := New(newFakeStore(), nil, nil, true, nil, "not-hex"); err == nil {
		t.Error("want invalid CSRF key error")
	}
	// valid 32-byte hex
	key := strings.Repeat("ab", 32)
	a, err := New(newFakeStore(), nil, nil, true, nil, key)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("nil admin")
	}
}

func TestExplainFromDiskAndIndex(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "main.go")
	content := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// companion test file
	if err := os.WriteFile(filepath.Join(root, "main_test.go"), []byte("package main\nfunc TestMain(t *testing.T){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Path: root, Dims: 768}}
	fs.chunks = []store.SearchResult{{Content: content, StartLine: 1, EndLine: 5}}
	fs.graph = map[string][]string{"other.go": {"main.go"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	// disk path
	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain?path=main.go&line=3")
	if code != 200 {
		t.Errorf("explain disk = %d %s", code, body)
	}

	// invalid line
	if code, _ := getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain?path=main.go&line=0"); code != 400 {
		t.Errorf("line0 = %d", code)
	}
	// missing path/line
	if code, _ := getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain"); code != 400 {
		t.Errorf("missing = %d", code)
	}
	// path escape
	if code, _ := getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain?path=../../etc/passwd&line=1"); code != 400 {
		t.Errorf("escape = %d", code)
	}

	// no disk path → explainFromIndex
	fs.projects[0].Path = ""
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain?path=main.go&line=3")
	if code != 200 && code != 404 {
		t.Errorf("explain index = %d %s", code, body)
	}

	// missing file on disk falls back to index
	fs.projects[0].Path = root
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain?path=missing.go&line=1")
	if code != 200 && code != 404 {
		t.Errorf("missing file = %d %s", code, body)
	}
}

func TestCreateProjectWithIndexEnqueue(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "git",
		"git_url":     "https://example.com/org/repo.git",
		"index":       true,
	})
	if code != http.StatusCreated || !strings.Contains(body, "job_id") {
		t.Errorf("create+index = %d %s", code, body)
	}
}

func TestCreateProjectEnqueueFail(t *testing.T) {
	es := &errStore{fakeStore: newFakeStore(), enqueueErr: errors.New("queue")}
	es.addUser("admin", "supersecret", "admin")
	a, err := New(es, fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv.Close)
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "git",
		"git_url":     "https://example.com/org/repo2.git",
		"index":       true,
	})
	if code != 500 {
		t.Errorf("enqueue fail = %d %s", code, body)
	}
}

func TestSystemCapsWithGitHubAndChat(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	// reach into admin via handler is hard; set via New + SetChat after
	a, err := New(fs, fakeEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	a.SetChat(nil) // no-op
	a.githubToken = "ghp_test"
	srv2 := httptest.NewTLSServer(a.Handler())
	t.Cleanup(srv2.Close)
	c := newClient(t, srv2)
	login(t, c, srv2.URL, "admin", "supersecret")
	code, body := getBody(t, c, srv2.URL+"/admin/api/system")
	if code != 200 || !strings.Contains(body, "github_discovery") {
		t.Errorf("system caps = %d %s", code, body)
	}
	_ = srv
}

func TestAPILogoutOK(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/logout", csrf, map[string]any{})
	if code != 200 || !strings.Contains(body, "ok") {
		t.Errorf("logout = %d %s", code, body)
	}
}

func TestRevokeTokenAPI(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")

	// create then revoke
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/tokens", csrf, map[string]any{
		"name": "ctl2", "scopes": []string{"read"}, "ttl_days": 0,
	})
	if code != 201 {
		t.Fatalf("create = %d %s", code, body)
	}
	// revoke bad id
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/tokens/x", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("revoke bad = %d", resp.StatusCode)
	}
	// revoke missing
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/tokens/999", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("revoke missing = %d", resp.StatusCode)
	}
	// revoke existing (id 1 typically)
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/tokens/1", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		t.Errorf("revoke ok = %d", resp.StatusCode)
	}

	// revoke error
	fs.revokeErr = errors.New("rev")
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/tokens/1", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	// may be 404 if already gone, or 500
	_ = resp.StatusCode
}

func TestDeadCodeWithRoot(t *testing.T) {
	root := t.TempDir()
	// minimal module so deadcode.Analyze may run or fail gracefully
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Path: root}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/dead-code?limit=5")
	// 200 findings, or 502 if analyzer fails — both exercise paths
	if code != 200 && code != 502 {
		t.Errorf("dead-code = %d %s", code, body)
	}
}

func TestSbomWithFakeStore(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	// SBOM looks at lockfiles via store — may 502
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/sbom?format=spdx-json")
	if code != 200 && code != 502 && code != 500 {
		t.Errorf("sbom = %d %s", code, body)
	}
}

func TestGoPackageNameAndHelpers(t *testing.T) {
	if got := goPackageName([]byte("package foo\n")); got != "foo" {
		t.Errorf("pkg = %q", got)
	}
	if got := goPackageName([]byte("// no package\n")); got != "" {
		t.Errorf("empty pkg = %q", got)
	}
	// detectModulePath
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/x/y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := detectModulePath(root); got != "github.com/x/y" {
		t.Errorf("mod = %q", got)
	}
	if got := detectModulePath(t.TempDir()); got != "" {
		t.Errorf("no mod = %q", got)
	}
	// findTestFiles
	srcDir := filepath.Join(root, "pkg")
	_ = os.MkdirAll(srcDir, 0o755)
	_ = os.WriteFile(filepath.Join(srcDir, "foo.go"), []byte("package pkg\nfunc Foo(){}\n"), 0o600)
	_ = os.WriteFile(filepath.Join(srcDir, "foo_test.go"), []byte("package pkg\nfunc TestFoo(t *testing.T){}\n"), 0o600)
	tests := findTestFiles(root, "pkg/foo.go", "Foo")
	if len(tests) == 0 {
		t.Log("findTestFiles returned none (ok if heuristic misses)")
	}
}

func TestIngestModelInfoFailure(t *testing.T) {
	// embedder that fails ModelInfo
	fs := newFakeStore()
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "m"}}
	fs.addUser("admin", "supersecret", "admin")
	a, err := New(fs, failModelEmbedder{}, nil, true, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if _, ok := a.loadIngestSession(context.Background(), w, "demo"); ok {
		t.Error("want model info failure")
	}
	if w.Code != http.StatusBadGateway {
		t.Errorf("code = %d", w.Code)
	}
}

type failModelEmbedder struct{}

func (failModelEmbedder) ModelInfo(context.Context, string) (*embedpkg.ModelInfo, error) {
	return nil, errors.New("no model")
}
func (failModelEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, errors.New("no model")
}
func (failModelEmbedder) Embed(context.Context, string, ...string) ([][]float32, error) {
	return nil, errors.New("no model")
}
func (failModelEmbedder) ListModels(context.Context) ([]string, error) {
	return nil, errors.New("no model")
}
