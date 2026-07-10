package webadmin

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestAPILoginJSON(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
		"username": "admin", "password": "supersecret",
	})
	if code != 200 || !strings.Contains(body, `"username":"admin"`) {
		t.Fatalf("login = %d body=%s", code, body)
	}
	csrf := extractJSONField(t, body, "csrf")
	if csrf == "" {
		t.Fatalf("missing csrf in login body=%s", body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/me")
	if code != 200 || !strings.Contains(body, `"role":"admin"`) {
		t.Fatalf("me = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/system")
	if code != 200 || !strings.Contains(body, `"product":"semidx"`) {
		t.Fatalf("system = %d body=%s", code, body)
	}

	code, _ = postAdminJSON(t, c, srv.URL+"/admin/api/logout", csrf, map[string]any{})
	if code != 200 {
		t.Fatalf("logout = %d", code)
	}
}

func TestAPIUnauthorized(t *testing.T) {
	srv, _ := newAdminWith(t, fakeEmbedder{}, nil)
	c := newClient(t, srv)
	code, body := getBody(t, c, srv.URL+"/admin/api/me")
	if code != 401 || !strings.Contains(body, "unauthorized") {
		t.Fatalf("unauth = %d body=%s", code, body)
	}
}

func TestAPIJobsDeleteReindex(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Status: "ready"}}
	fs.jobs = []store.Job{{ID: 7, ProjectID: 1, Status: "done", Type: "full"}}
	fs.fileHashes = map[string]string{"a.go": "h1"}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := getBody(t, c, srv.URL+"/admin/api/jobs?limit=5")
	if code != 200 || !strings.Contains(body, `"id":7`) {
		t.Fatalf("all jobs = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/status")
	if code != 200 || !strings.Contains(body, `"total_files":1`) {
		t.Fatalf("status = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/jobs")
	if code != 200 {
		t.Fatalf("project jobs = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/reindex", csrf, map[string]any{"type": "full"})
	if code != http.StatusAccepted || !strings.Contains(body, `"job_id"`) {
		t.Fatalf("reindex = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/jobs/7?project=demo")
	if code != 200 || !strings.Contains(body, `"id":7`) {
		t.Fatalf("get job = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/jobs/7")
	if code != 200 {
		t.Fatalf("project job = %d body=%s", code, body)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/projects/demo", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("delete = %d body=%s", resp.StatusCode, b)
	}
}

func TestIngestArchiveAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile("archive", "drop.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(makeTestZip(t, map[string]string{"pkg/main.go": "package main\n"})); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/api/projects/demo/files/archive", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(respBody), `"indexed":1`) {
		t.Fatalf("archive ingest = %d body=%s", resp.StatusCode, respBody)
	}
}

func extractJSONField(t *testing.T, body, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
