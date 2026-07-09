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
