package webadmin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestAPILoginFailures(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
		"username": "admin", "password": "wrong",
	})
	if code != 401 || !strings.Contains(body, "invalid username") {
		t.Fatalf("bad password = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{
		"username": "nobody", "password": "x",
	})
	if code != 401 {
		t.Fatalf("unknown user = %d body=%s", code, body)
	}

	code, _ = postAdminJSON(t, c, srv.URL+"/admin/api/login", "", map[string]any{"username": "admin"})
	if code != 401 {
		t.Fatalf("empty password = %d", code)
	}
}

func TestAPICreateProjectValidation(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "git",
	})
	if code != 400 || !strings.Contains(body, "git_url") {
		t.Fatalf("missing git_url = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "push", "name": "docs",
	})
	if code != http.StatusCreated || !strings.Contains(body, `"push_hint"`) {
		t.Fatalf("push project = %d body=%s", code, body)
	}

	fs.createErr = store.ErrProjectExists
	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/projects", csrf, map[string]any{
		"source_type": "git", "git_url": "https://example.com/x.git",
	})
	if code != 409 {
		t.Fatalf("conflict = %d body=%s", code, body)
	}
}

func TestAPIReindexValidation(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/projects/demo/reindex", csrf, map[string]any{"type": "bad"})
	if code != 400 || !strings.Contains(body, "git_history") {
		t.Fatalf("bad reindex type = %d body=%s", code, body)
	}
}

func TestAPISearchAndListProjects(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Dims: 3}}
	fs.searchProject = &store.Project{ID: 1, Name: "demo", Model: "bge-m3", Dims: 3}
	fs.searchResults = []store.SearchResult{{FilePath: "a.go", StartLine: 1, EndLine: 2, Content: "func main", Score: 0.9}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects?limit=10")
	if code != 200 || !strings.Contains(body, `"demo"`) {
		t.Fatalf("list projects = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/search", csrf, map[string]any{
		"query": "main function", "project": "demo", "top": 5,
	})
	if code != 200 || !strings.Contains(body, `"a.go"`) {
		t.Fatalf("search = %d body=%s", code, body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/search", csrf, map[string]any{"query": ""})
	if code != 400 {
		t.Fatalf("empty search = %d body=%s", code, body)
	}
}

func TestAPIJobErrors(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/jobs/1")
	if code != 400 || !strings.Contains(body, "project query") {
		t.Fatalf("job without project = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/jobs/0?project=demo")
	if code != 400 {
		t.Fatalf("invalid job id = %d body=%s", code, body)
	}
}

func TestAPIDeleteProjectNotFound(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/api/projects/missing", nil)
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("delete missing = %d", resp.StatusCode)
	}
}

func TestJobToJSONFailed(t *testing.T) {
	body := jobToJSON(&store.Job{Status: "failed", Error: "secret detail"})
	if !strings.Contains(body["error"].(string), "failed") || strings.Contains(body["error"].(string), "secret") {
		t.Fatalf("sanitized error = %#v", body)
	}
}

func TestWriteScopedJobWrongProject(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	fs.jobs = []store.Job{{ID: 7, ProjectID: 1, Status: "done", Type: "full"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/other/jobs/7")
	if code != 404 || !strings.Contains(body, "not found") {
		t.Fatalf("wrong project job = %d body=%s", code, body)
	}
}
