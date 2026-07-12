package webadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestFilterSortedPaths(t *testing.T) {
	hashes := map[string]string{
		"src/a.go":  "h1",
		"src/b.go":  "h2",
		"docs/x.md": "h3",
	}
	got := filterSortedPaths(hashes, "src/", "b")
	if len(got) != 1 || got[0] != "src/b.go" {
		t.Fatalf("filterSortedPaths = %#v", got)
	}
	all := filterSortedPaths(hashes, "", "")
	if len(all) != 3 || all[0] != "docs/x.md" {
		t.Fatalf("sorted = %#v", all)
	}
}

func TestNormalizeCreateProjectBody(t *testing.T) {
	t.Run("git derives name", func(t *testing.T) {
		body := createProjectBody{SourceType: "git", GitURL: "https://example.com/foo.git"}
		name, status, msg := normalizeCreateProjectBody(&body)
		if msg != "" || status != 0 || name != "foo" || body.Branch != "main" {
			t.Fatalf("name=%q status=%d msg=%q branch=%q", name, status, msg, body.Branch)
		}
	})
	t.Run("push requires name", func(t *testing.T) {
		body := createProjectBody{SourceType: "push"}
		_, status, msg := normalizeCreateProjectBody(&body)
		if status != http.StatusBadRequest || !strings.Contains(msg, "name is required") {
			t.Fatalf("status=%d msg=%q", status, msg)
		}
	})
	t.Run("invalid source type", func(t *testing.T) {
		body := createProjectBody{SourceType: "svn", Name: "x"}
		_, status, msg := normalizeCreateProjectBody(&body)
		if status != http.StatusBadRequest || !strings.Contains(msg, "source_type") {
			t.Fatalf("status=%d msg=%q", status, msg)
		}
	})
	t.Run("push with name", func(t *testing.T) {
		body := createProjectBody{SourceType: "push", Name: "docs"}
		name, status, msg := normalizeCreateProjectBody(&body)
		if msg != "" || status != 0 || name != "docs" || body.Index {
			t.Fatalf("push name=%q index=%v", name, body.Index)
		}
	})
	t.Run("invalid derived name", func(t *testing.T) {
		body := createProjectBody{SourceType: "git", GitURL: "https://example.com/.git", Name: "."}
		_, status, msg := normalizeCreateProjectBody(&body)
		if status != http.StatusBadRequest {
			t.Fatalf("status=%d msg=%q", status, msg)
		}
	})
}

func TestBuildFileChunkResponse(t *testing.T) {
	chunks := []store.SearchResult{
		{StartLine: 1, EndLine: 2, Content: "line1"},
		{StartLine: 3, EndLine: 4, Content: "line2"},
	}
	out, content, truncated := buildFileChunkResponse(chunks, 64)
	if truncated || content != "line1\nline2" || len(out) != 2 {
		t.Fatalf("out=%#v content=%q truncated=%v", out, content, truncated)
	}
}

func TestFetchProjectFileChunks(t *testing.T) {
	fs := newFakeStore()
	fs.chunks = []store.SearchResult{{Content: "ok", StartLine: 1, EndLine: 1}}
	chunks, dims, err := fetchProjectFileChunks(context.Background(), fs, 1, "a.go", 1024)
	if err != nil || dims != 1024 || len(chunks) != 1 {
		t.Fatalf("chunks=%v dims=%d err=%v", chunks, dims, err)
	}
}

func postAdminJSON(t *testing.T, c *http.Client, url, csrf string, body any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestProjectFilesAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	fs.fileHashes = map[string]string{"src/a.go": "abc", "docs/b.md": "def"}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/files?prefix=src/&q=a")
	if code != 200 || !strings.Contains(body, `"path":"src/a.go"`) || strings.Contains(body, "docs/b.md") {
		t.Fatalf("files api = %d body=%s", code, body)
	}
}

func TestProjectFileContentAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Dims: 1024}}
	fs.chunks = []store.SearchResult{{Content: "hello", StartLine: 1, EndLine: 1}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/files/content?path=main.go")
	if code != 200 || !strings.Contains(body, `"content":"hello"`) {
		t.Fatalf("content api = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/files/content?path=../etc/passwd")
	if code != 400 {
		t.Fatalf("traversal = %d body=%s", code, body)
	}
}

func TestCreateProjectAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "git",
		"git_url":     "https://example.com/newrepo.git",
		"index":       true,
	})
	if code != http.StatusCreated || !strings.Contains(body, `"name":"newrepo"`) || !strings.Contains(body, `"job_id":`) {
		t.Fatalf("create project = %d body=%s", code, body)
	}
	if len(fs.projects) != 1 {
		t.Fatalf("projects = %d", len(fs.projects))
	}
}
