package webadmin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/jwtauth"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestSettingsUsersValidation(t *testing.T) {
	iss, _ := jwtauth.New("test-secret")
	srv, fs := newAdminWith(t, fakeEmbedder{}, iss)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/users", csrf, map[string]any{
		"username": "", "password": "short",
	})
	if code != 400 || !strings.Contains(body, "at least 8 characters") {
		t.Fatalf("short password = %d body=%s", code, body)
	}

	fs.addUser("bob", "bobsecret1", "member")
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/users", csrf, map[string]any{
		"username": "bob", "password": "bobsecret1", "role": "member",
	})
	if code != 409 {
		t.Fatalf("duplicate user = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/users/1/disabled", csrf, map[string]any{"disabled": true})
	if code != 400 || !strings.Contains(body, "you cannot disable your own account") {
		t.Fatalf("self disable = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/users/999/disabled", csrf, map[string]any{"disabled": true})
	if code != 404 {
		t.Fatalf("missing user = %d body=%s", code, body)
	}
}

func TestSettingsCreateKeyValidation(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("m", "memberpass", "member")
	c := newClient(t, srv)
	login(t, c, srv.URL, "m", "memberpass")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/keys", csrf, map[string]any{"name": "", "scopes": []string{"read"}})
	if code != 400 || !strings.Contains(body, "name is required") {
		t.Fatalf("empty key name = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/keys", csrf, map[string]any{"name": "x", "scopes": []string{"admin"}})
	if code != 400 || !strings.Contains(body, "only admins") {
		t.Fatalf("member admin scope = %d body=%s", code, body)
	}
}

func TestProjectDetailEnriched(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Dims: 3, Status: "ready"}}
	fs.projectCommit = "abc123"
	fs.jobs = []store.Job{{ID: 2, ProjectID: 1, Status: "done", Type: "full", FilesIndexed: 5}}
	fs.fileHashes = map[string]string{"main.go": "h1", "README.md": "h2"}
	fs.chunkCount = 11
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo")
	if code != 200 || !strings.Contains(body, `"last_commit":"abc123"`) || !strings.Contains(body, `"total_chunks":11`) {
		t.Fatalf("detail = %d body=%s", code, body)
	}
}

func TestProjectFilesPagination(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	fs.fileHashes = map[string]string{"a.go": "1", "b.go": "2", "c.go": "3"}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/files?limit=1&offset=1")
	if code != 200 || !strings.Contains(body, `"total":3`) || !strings.Contains(body, `"offset":1`) {
		t.Fatalf("pagination = %d body=%s", code, body)
	}
}

func TestAPILoginRememberMe(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
		"username": "admin", "password": "supersecret", "remember_me": true,
	})
	if code != 200 || !strings.Contains(body, `"csrf"`) {
		t.Fatalf("remember login = %d body=%s", code, body)
	}
}

func TestIngestHelpersUnit(t *testing.T) {
	if got := sanitizeIngestIndexError(errors.New("boom")); got == "" {
		t.Fatal("expected sanitized message")
	}
	if _, status, msg := openZipReader([]byte("not-a-zip")); msg == "" || status != http.StatusBadRequest {
		t.Fatalf("bad zip = %d %q", status, msg)
	}

	fs := newFakeStore()
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	rec := httptest.NewRecorder()
	_, ok := a.loadIngestSession(context.Background(), rec, "missing")
	if ok || rec.Code != http.StatusNotFound {
		t.Fatalf("missing project loadIngest = %d ok=%v", rec.Code, ok)
	}

	deleted := a.ingestDeletePaths(context.Background(), 1, []string{"old.go", "../bad"})
	if deleted != 1 {
		t.Fatalf("deleted=%d", deleted)
	}
}

func TestIngestIndexFileListUTF8(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	idx := indexing.NewIndexer(fs, fakeEmbedder{}, 3, indexing.IndexerOpts{})
	files := []ingestFile{
		{Path: "ok.go", Content: "package ok\n"},
		{Path: "bad.go", Content: string([]byte{0xff, 0xfe, 0xfd})},
	}
	res := ingestIndexFileList(context.Background(), testLogger(t), idx, 1, "m", files)
	if res.indexed != 1 || res.errs != 1 {
		t.Fatalf("res=%+v", res)
	}
}

func TestCountChunksUnsupported(t *testing.T) {
	var st store.Store // nil concrete — no chunkCounter
	_, err := countChunks(context.Background(), st, 1, 3)
	if err == nil {
		t.Fatal("expected unsupported")
	}
}

func TestEnrichProjectWithChunks(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Dims: 3}}
	fs.chunkCount = 7
	fs.projectCommit = "deadbeef"
	a, _ := New(fs, fakeEmbedder{}, nil, true, nil, "")
	item := a.enrichProject(context.Background(), fs.projects[0], true, false)
	if item.TotalChunks == nil || *item.TotalChunks != 7 || item.LastCommit != "deadbeef" {
		t.Fatalf("item=%+v", item)
	}
}

func TestSettingsChangePassword(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/account/password", csrf, map[string]any{
		"current": "wrong", "new": "newsecret1",
	})
	if code != 400 || !strings.Contains(body, "incorrect") {
		t.Fatalf("bad current = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/account/password", csrf, map[string]any{
		"current": "supersecret", "new": "short",
	})
	if code != 400 || !strings.Contains(body, "at least 8 characters") {
		t.Fatalf("short new = %d body=%s", code, body)
	}
}

func TestSettingsCreateKeySuccess(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/keys", csrf, map[string]any{
		"name": "ci-key", "scopes": []string{"read", "write"},
	})
	if code != 201 || !strings.Contains(body, `"token"`) {
		t.Fatalf("create key = %d body=%s", code, body)
	}
}

func TestCreateProjectGitWithIndex(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"name": "from-git", "source_type": "git",
		"git_url": "https://example.com/repo.git", "branch": "main", "index": true,
	})
	if code != 201 || !strings.Contains(body, `"job_id"`) {
		t.Fatalf("create git+index = %d body=%s", code, body)
	}
}

func TestCreateProjectPushHint(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"name": "push-proj", "source_type": "push",
	})
	if code != 201 || !strings.Contains(body, `"push_hint"`) {
		t.Fatalf("create push = %d body=%s", code, body)
	}
}
