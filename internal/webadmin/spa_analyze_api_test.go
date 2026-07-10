package webadmin

import (
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestAnalyzeCallersAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/callers?path=internal/auth/token.go")
	if code != 200 || !strings.Contains(body, `"callers"`) {
		t.Fatalf("callers = %d body=%s", code, body)
	}

	code, body = getBody(t, c, srv.URL+"/admin/api/projects/demo/deps?path=internal/auth/token.go")
	if code != 200 || !strings.Contains(body, `"dependencies"`) {
		t.Fatalf("deps = %d body=%s", code, body)
	}
}

func TestAnalyzeCallersMissingPath(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/callers")
	if code != 400 || !strings.Contains(body, "path is required") {
		t.Fatalf("callers missing path = %d body=%s", code, body)
	}
}

func TestAnalyzeDepsWithGraph(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	fs.graph = map[string][]string{"main.go": {"internal/auth/"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/deps?path=main.go")
	if code != 200 || !strings.Contains(body, `"internal/auth/"`) {
		t.Fatalf("deps graph = %d body=%s", code, body)
	}
}

func TestGraphStatsAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	// a -> b, c ; b -> c  => nodes {a,b,c}=3, edges=3, c most depended (in=2).
	fs.graph = map[string][]string{"a.go": {"b.go", "c.go"}, "b.go": {"c.go"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/graph-stats")
	if code != 200 {
		t.Fatalf("graph-stats = %d body=%s", code, body)
	}
	for _, want := range []string{`"nodes":3`, `"edges":3`, `"top_depends"`, `"top_depended"`, `"c.go"`} {
		if !strings.Contains(body, want) {
			t.Errorf("graph-stats body missing %q:\n%s", want, body)
		}
	}
}

func TestGraphStatsProjectNotFound(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, _ := getBody(t, c, srv.URL+"/admin/api/projects/ghost/graph-stats")
	if code != 404 {
		t.Fatalf("graph-stats missing project = %d, want 404", code)
	}
}

func TestAnalyzeDeadCodeMissingRoot(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/dead-code")
	if code != 400 || !strings.Contains(body, "project has no path") {
		t.Fatalf("dead-code no root = %d body=%s", code, body)
	}
}
