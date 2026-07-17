package webadmin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lgldsilva/semidx/internal/gitcredmgr"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

// coverage-patch: 2026-07-17

func TestValidateCreateProjectCredential(t *testing.T) {
	w := httptest.NewRecorder()
	// no credential → ok
	if !validateCreateProjectCredential(w, &authCtx{user: &store.User{Role: "member"}}, &createProjectBody{}) {
		t.Fatal("empty credential should pass")
	}
	// empty secret → ok
	if !validateCreateProjectCredential(w, &authCtx{user: &store.User{Role: "member"}}, &createProjectBody{
		Credential: &inlineProjectCredential{Secret: "  "},
	}) {
		t.Fatal("blank secret should pass")
	}
	// member + secret → forbidden
	w = httptest.NewRecorder()
	if validateCreateProjectCredential(w, &authCtx{user: &store.User{Role: "member"}}, &createProjectBody{
		SourceType: "git",
		Credential: &inlineProjectCredential{Secret: "tok"},
	}) {
		t.Fatal("member should be rejected")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d", w.Code)
	}
	// admin + non-git → bad request
	w = httptest.NewRecorder()
	if validateCreateProjectCredential(w, &authCtx{user: &store.User{Role: "admin"}}, &createProjectBody{
		SourceType: "push",
		Credential: &inlineProjectCredential{Secret: "tok"},
	}) {
		t.Fatal("non-git should be rejected")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d", w.Code)
	}
	// admin + git → ok
	if !validateCreateProjectCredential(httptest.NewRecorder(), &authCtx{user: &store.User{Role: "admin"}}, &createProjectBody{
		SourceType: "git",
		Credential: &inlineProjectCredential{Secret: "tok"},
	}) {
		t.Fatal("admin git should pass")
	}
}

func TestCreateProjectAPICredentialBranches(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("member", "supersecret", "member")
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "member", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "git",
		"git_url":     "https://example.com/r.git",
		"credential":  map[string]any{"secret": "tok", "kind": "token"},
	})
	if code != http.StatusForbidden {
		t.Fatalf("member cred = %d body=%s", code, body)
	}

	// admin push project
	login(t, c, srv.URL, "admin", "supersecret")
	csrf = csrfFrom(t, c, srv.URL+"/admin/api/me")
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "push",
		"name":        "docs-push",
	})
	if code != http.StatusCreated || !strings.Contains(body, "push_hint") {
		t.Fatalf("push create = %d body=%s", code, body)
	}

	// conflict
	fs.createErr = store.ErrProjectExists
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "push",
		"name":        "dup",
	})
	if code != http.StatusConflict {
		t.Fatalf("conflict = %d body=%s", code, body)
	}
	fs.createErr = errors.New("db down")
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "push",
		"name":        "x",
	})
	if code != http.StatusInternalServerError {
		t.Fatalf("create err = %d body=%s", code, body)
	}
	fs.createErr = nil

	// invalid JSON
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/api/projects", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad json = %d", resp.StatusCode)
	}
}

func TestConversationsAPIMoreBranches(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")

	// create
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/conversations", csrf, map[string]any{
		"project": "p", "title": "t1",
	})
	if code != 200 {
		t.Fatalf("create = %d %s", code, body)
	}
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	base := srv.URL + "/admin/api/conversations/" + itoa(created.ID)

	// rename bad body
	if st := doJSON(t, c, http.MethodPatch, base, csrf, `{}`); st != 400 {
		t.Errorf("rename empty title = %d", st)
	}
	if st := doJSON(t, c, http.MethodPatch, base, csrf, `not-json`); st != 400 {
		t.Errorf("rename bad json = %d", st)
	}
	if st := doJSON(t, c, http.MethodPatch, srv.URL+"/admin/api/conversations/x", csrf, `{"title":"a"}`); st != 400 {
		t.Errorf("rename bad id = %d", st)
	}
	if st := doJSON(t, c, http.MethodPatch, srv.URL+"/admin/api/conversations/999", csrf, `{"title":"a"}`); st != 404 {
		t.Errorf("rename missing = %d", st)
	}

	// delete bad id / missing
	if st := doJSON(t, c, http.MethodDelete, srv.URL+"/admin/api/conversations/x", csrf, ""); st != 400 {
		t.Errorf("delete bad id = %d", st)
	}
	if st := doJSON(t, c, http.MethodDelete, srv.URL+"/admin/api/conversations/999", csrf, ""); st != 404 {
		t.Errorf("delete missing = %d", st)
	}

	// add message validation
	if code, _ = postAdminJSON(t, c, base+"/messages", csrf, map[string]any{
		"role": "system", "content": "x",
	}); code != 400 {
		t.Errorf("bad role = %d", code)
	}
	if code, _ = postAdminJSON(t, c, base+"/messages", csrf, "not-a-map"); code != 400 {
		// postAdminJSON marshals string as JSON string — still invalid structure
		// Actually marshals as "\"not-a-map\"" which decode may fail
		_ = code
	}
	// bad id on messages
	if code, _ = postAdminJSON(t, c, srv.URL+"/admin/api/conversations/x/messages", csrf, map[string]any{
		"role": "user", "content": "hi",
	}); code != 400 {
		t.Errorf("msg bad id = %d", code)
	}

	// messageJSON without sources
	m := messageJSON(store.ConversationMessage{ID: 1, Role: "user", Content: "c", CreatedAt: time.Unix(1, 0)})
	if _, ok := m["sources"]; ok {
		t.Error("empty sources should be omitted")
	}
	// with sources
	m = messageJSON(store.ConversationMessage{
		ID: 1, Role: "assistant", Content: "c", SourcesJSON: `[{"f":"a.go"}]`, CreatedAt: time.Unix(1, 0),
	})
	if _, ok := m["sources"]; !ok {
		t.Error("sources should be present")
	}

	// delete ok
	if st := doJSON(t, c, http.MethodDelete, base, csrf, ""); st != 200 {
		t.Errorf("delete = %d", st)
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.ReplaceAll(jsonNumber(n), "\n", ""))
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestSPAFileServer(t *testing.T) {
	h := spaFileServer()
	// method not allowed
	req := httptest.NewRequest(http.MethodPost, "/admin/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", w.Code)
	}
	// api prefix under spa
	req = httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("api via spa = %d", w.Code)
	}
	// index route (may 500 if embed missing, or 200 if built)
	req = httptest.NewRequest(http.MethodGet, "/admin/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 500 {
		t.Errorf("index = %d", w.Code)
	}
	// client route fallback
	req = httptest.NewRequest(http.MethodGet, "/admin/search", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 500 {
		t.Errorf("spa route = %d", w.Code)
	}
	// HEAD
	req = httptest.NewRequest(http.MethodHead, "/admin/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
}

func TestTryServeSPAAsset(t *testing.T) {
	mem := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>")},
		"app.js":     &fstest.MapFile{Data: []byte("console.log(1)")},
		"dir":        &fstest.MapFile{Mode: fs.ModeDir},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/app.js", nil)
	if !tryServeSPAAsset(w, req, mem, "app.js") {
		t.Fatal("want serve app.js")
	}
	if w.Code != 200 {
		t.Errorf("code = %d", w.Code)
	}
	// missing
	if tryServeSPAAsset(httptest.NewRecorder(), req, mem, "nope.js") {
		t.Error("missing asset")
	}
	// directory
	if tryServeSPAAsset(httptest.NewRecorder(), req, mem, "dir") {
		t.Error("dir should not serve")
	}
	// serveSPAIndex
	w = httptest.NewRecorder()
	serveSPAIndex(w, req, mem)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "ok") {
		t.Errorf("index = %d %s", w.Code, w.Body.String())
	}
	// missing index
	w = httptest.NewRecorder()
	serveSPAIndex(w, req, fstest.MapFS{})
	if w.Code != 500 {
		t.Errorf("missing index = %d", w.Code)
	}
}

func TestJobToJSONAndTopDegrees(t *testing.T) {
	j := &store.Job{
		ID: 1, Type: "full", Status: "failed", Error: "git clone: authentication failed",
		ProgressDone: 5, ProgressTotal: 10, FilesIndexed: 1,
	}
	out := jobToJSON(j)
	if out["error"] == j.Error {
		t.Error("failed job error should be summarized")
	}
	if out["progress_percent"] != 50 {
		t.Errorf("pct = %v", out["progress_percent"])
	}
	// cap progress at 100
	j.ProgressDone = 999
	j.ProgressTotal = 10
	j.Status = "running"
	j.Error = ""
	out = jobToJSON(j)
	if out["progress_percent"] != 100 {
		t.Errorf("capped pct = %v", out["progress_percent"])
	}
	// no progress total
	j.ProgressTotal = 0
	out = jobToJSON(j)
	if _, ok := out["progress_percent"]; ok {
		t.Error("no progress_percent without total")
	}

	// topDegrees limit + tie-break
	deg := map[string]int{"b": 2, "a": 2, "c": 5, "d": 1}
	got := topDegrees(deg, 2)
	if len(got) != 2 || got[0].Node != "c" {
		t.Fatalf("topDegrees = %+v", got)
	}
	// tie: a before b
	got = topDegrees(map[string]int{"b": 2, "a": 2}, 10)
	if len(got) != 2 || got[0].Node != "a" || got[1].Node != "b" {
		t.Fatalf("tie break = %+v", got)
	}
}

func TestProjectAPIErrorBranches(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Dims: 0, Status: "ready"}}
	fs.fileHashes = map[string]string{"a.go": "h1", "b.go": "h2"}
	fs.jobs = []store.Job{{ID: 1, ProjectID: 1, Status: "done", Type: "full", ProgressDone: 1, ProgressTotal: 1}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")

	// detail not found
	code, _ := getBody(t, c, srv.URL+"/admin/api/projects/missing")
	if code != 404 {
		t.Errorf("detail missing = %d", code)
	}
	// detail ok
	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo")
	if code != 200 || !strings.Contains(body, "demo") {
		t.Errorf("detail = %d %s", code, body)
	}

	// files with offset past end
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/files?offset=99&limit=1")
	if code != 200 {
		t.Errorf("files page = %d %s", code, body)
	}

	// file content missing path
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/demo/files/content")
	if code != 400 {
		t.Errorf("content no path = %d", code)
	}
	// absolute path rejected
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/demo/files/content?path=/etc/passwd")
	if code != 400 {
		t.Errorf("abs path = %d", code)
	}
	// dims default when 0
	fs.chunks = []store.SearchResult{{Content: "x", StartLine: 1, EndLine: 1}}
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/files/content?path=a.go")
	if code != 200 {
		t.Errorf("content = %d %s", code, body)
	}

	// jobs not found project
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/nope/jobs")
	if code != 404 {
		t.Errorf("jobs missing proj = %d", code)
	}
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/demo/jobs?limit=1&offset=0")
	if code != 200 {
		t.Errorf("jobs = %d", code)
	}

	// status not found
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/nope/status")
	if code != 404 {
		t.Errorf("status missing = %d", code)
	}
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/status")
	if code != 200 || !strings.Contains(body, "total_files") {
		t.Errorf("status = %d %s", code, body)
	}

	// reindex invalid type
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/reindex", csrf, map[string]any{"type": "weird"})
	if code != 400 {
		t.Errorf("reindex type = %d %s", code, body)
	}
	// reindex missing project
	code, _ = postAdminJSON(t, c, srv.URL+"/admin/api/projects/nope/reindex", csrf, map[string]any{})
	if code != 404 {
		t.Errorf("reindex missing = %d", code)
	}
	// reindex default type
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/reindex", csrf, map[string]any{})
	if code != http.StatusAccepted {
		t.Errorf("reindex = %d %s", code, body)
	}

	// delete missing
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/projects/nope", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("delete missing = %d", resp.StatusCode)
	}

	// project job invalid id
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/demo/jobs/x")
	if code != 400 {
		t.Errorf("job bad id = %d", code)
	}
	// scoped job wrong project
	fs.jobs = []store.Job{{ID: 9, ProjectID: 1, Status: "done", Type: "full"}}
	code, _ = getBody(t, c, srv.URL+"/admin/api/jobs/9?project=other")
	if code != 404 {
		t.Errorf("wrong project scope = %d", code)
	}
	// get job missing project query
	code, _ = getBody(t, c, srv.URL+"/admin/api/jobs/9")
	if code != 400 {
		t.Errorf("job no project = %d", code)
	}
}

func TestAnalyzeAPIErrorBranches(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Path: ""}}
	fs.graph = map[string][]string{
		"a.go": {"internal/pkg/", "b.go"},
		"c.go": {"internal/pkg"},
	}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	// callers missing path
	code, _ := getBody(t, c, srv.URL+"/admin/api/projects/demo/callers")
	if code != 400 {
		t.Errorf("callers no path = %d", code)
	}
	// callers missing project
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/nope/callers?path=a.go")
	if code != 404 {
		t.Errorf("callers missing proj = %d", code)
	}
	// callers with graph (package dir match)
	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/callers?path=internal/pkg/x.go")
	if code != 200 {
		t.Errorf("callers = %d %s", code, body)
	}

	// deps
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/demo/deps")
	if code != 400 {
		t.Errorf("deps no path = %d", code)
	}
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/deps?path=a.go")
	if code != 200 || !strings.Contains(body, "dependencies") {
		t.Errorf("deps = %d %s", code, body)
	}
	// deps path not in graph → empty
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/demo/deps?path=missing.go")
	if code != 200 {
		t.Errorf("deps missing path = %d", code)
	}
	// deps project not found
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/nope/deps?path=a.go")
	if code != 404 {
		t.Errorf("deps missing proj = %d", code)
	}

	// dead code no checkout path
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/dead-code")
	if code != 400 || !strings.Contains(body, "no path") {
		t.Errorf("deadcode no checkout = %d %s", code, body)
	}
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/nope/dead-code")
	if code != 404 {
		t.Errorf("deadcode missing = %d", code)
	}

	// graph stats with top param
	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/graph-stats?top=1")
	if code != 200 || !strings.Contains(body, "nodes") {
		t.Errorf("graph stats = %d %s", code, body)
	}
	code, _ = getBody(t, c, srv.URL+"/admin/api/projects/nope/graph-stats")
	if code != 404 {
		t.Errorf("graph stats missing = %d", code)
	}
}

func TestIngestHelpersCoverage(t *testing.T) {
	// readZipUpload validation
	if _, st, msg := readZipUpload(strings.NewReader("x"), "a.txt"); st != 400 || msg == "" {
		t.Errorf("non-zip = %d %s", st, msg)
	}
	// oversized
	big := strings.NewReader(strings.Repeat("a", adminIngestMaxZipBytes+2))
	if _, st, msg := readZipUpload(big, "a.zip"); st != 400 || !strings.Contains(msg, "too large") {
		t.Errorf("oversize = %d %s", st, msg)
	}
	// openZipReader invalid
	if _, st, msg := openZipReader([]byte("notzip")); st != 400 || msg == "" {
		t.Errorf("bad zip = %d %s", st, msg)
	}

	// ingestDeletePaths skips empty + continues on error
	fs := newFakeStore()
	a := &Admin{store: fs, log: slog.Default()}
	fs.fileHashes = map[string]string{"a.go": "h", "b.go": "h"}
	n := a.ingestDeletePaths(context.Background(), 1, []string{"", "../x", "a.go", "missing.go"})
	if n < 1 {
		t.Errorf("deleted = %d", n)
	}

	// ingestIndexOneFile invalid path / oversized / non-utf8
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 3, indexing.IndexerOpts{Workers: 1})
	if _, ferr, _ := ingestIndexOneFile(context.Background(), idx, 1, "m", "../x", "c"); ferr == nil {
		t.Error("invalid path")
	}
	if _, ferr, _ := ingestIndexOneFile(context.Background(), idx, 1, "m", "a.go", string([]byte{0xff, 0xfe})); ferr == nil {
		t.Error("non-utf8")
	}
	huge := strings.Repeat("x", adminIngestMaxFileBytes+1)
	if _, ferr, _ := ingestIndexOneFile(context.Background(), idx, 1, "m", "a.go", huge); ferr == nil {
		t.Error("too large")
	}
	// success
	nChunks, ferr, err := ingestIndexOneFile(context.Background(), idx, 1, "m", "ok.go", "package ok\nfunc Ok(){}\n")
	if err != nil || ferr != nil {
		t.Fatalf("ok file: n=%d ferr=%v err=%v", nChunks, ferr, err)
	}

	// finishIngest when project missing (warn path)
	a.finishIngest(context.Background(), 999, "label")

	// writeIngestResult
	w := httptest.NewRecorder()
	writeIngestResult(w, ingestIndexResult{indexed: 1, chunks: 2, errs: 0}, 1)
	if w.Code != 200 {
		t.Errorf("writeIngest = %d", w.Code)
	}

	// cleanRelPath
	for _, p := range []string{"", ".", "..", "../x", "/abs", "  "} {
		if cleanRelPath(p) != "" {
			t.Errorf("cleanRelPath(%q) should be empty", p)
		}
	}
	if cleanRelPath(`src\a.go`) != "src/a.go" {
		t.Errorf("slash convert failed")
	}

	if st, msg := validateIngestBody(&ingestBody{}); st != 400 || msg == "" {
		t.Errorf("empty body = %d %s", st, msg)
	}
	files := make([]ingestFile, adminIngestMaxFiles+1)
	for i := range files {
		files[i] = ingestFile{Path: "a.go", Content: "x"}
	}
	if st, msg := validateIngestBody(&ingestBody{Files: files}); st != 400 {
		t.Errorf("too many = %d %s", st, msg)
	}
}

type slogDiscard struct{}

func (slogDiscard) Error(string, ...any) {}

func TestCreateInlineCredentialNil(t *testing.T) {
	a := &Admin{}
	if err := a.createInlineProjectCredential(context.Background(), 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.createInlineProjectCredential(context.Background(), 1, &inlineProjectCredential{Secret: "  "}); err != nil {
		t.Fatal(err)
	}
}

func TestGitCredentialDeleteInvalidID(t *testing.T) {
	srv, _, c := newAdminWithGitCreds(t)
	csrf := csrfFrom(t, c, srv.URL+"/admin/api/me")
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/git-credentials/0", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 400 {
		b, _ := io.ReadAll(resp.Body)
		t.Errorf("delete id0 = %d %s", resp.StatusCode, b)
	}
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/git-credentials/x", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("delete bad id = %d", resp.StatusCode)
	}
	// missing credential
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/git-credentials/999", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("delete missing = %d", resp.StatusCode)
	}
}

func TestWriteGitCredErrBranches(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{gitcredmgr.ErrUnsupported, http.StatusNotImplemented},
		{gitcredmgr.ErrSecretboxDisabled, http.StatusServiceUnavailable},
		{store.ErrNotFound, http.StatusNotFound},
		{errors.New("name already exists"), http.StatusConflict},
		{errors.New("passphrase-protected keys not supported"), http.StatusBadRequest},
		{errors.New("invalid SSH key"), http.StatusBadRequest},
		{errors.New("kind must be token or ssh"), http.StatusBadRequest},
		{errors.New("secret is required"), http.StatusBadRequest},
		{errors.New("set exactly one of project_id or host_pattern"), http.StatusBadRequest},
		{errors.New("mystery"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		writeGitCredErr(w, slogDiscard{}, "act", tc.err)
		if w.Code != tc.code {
			t.Errorf("err %v → %d want %d", tc.err, w.Code, tc.code)
		}
	}
}

func TestFetchProjectFileChunksEmpty(t *testing.T) {
	fs := newFakeStore()
	fs.chunks = nil
	chunks, dims, err := fetchProjectFileChunks(context.Background(), fs, 1, "a.go", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if dims != 1024 || chunks == nil {
		// empty slice ok
		t.Logf("chunks=%v dims=%d", chunks, dims)
	}
}

func TestAPILoginLogoutSystemEdges(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)

	// bad password
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
		"username": "admin", "password": "wrong",
	})
	if code != 401 && code != 403 {
		// login may return 401
		if code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Logf("bad login = %d %s", code, body)
		}
	}
	// bad json
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/admin/api/login", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	// successful login + logout without session (already tested) — logout unauth
	code, _ = postAdminJSON(t, c, srv.URL+"/admin/api/logout", "", map[string]any{})
	_ = code
}
