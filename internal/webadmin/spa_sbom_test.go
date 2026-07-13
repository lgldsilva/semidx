package webadmin

import (
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestProjectSbomAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/sbom")
	if code != 200 || !strings.Contains(body, `"component_count"`) {
		t.Fatalf("sbom = %d body=%s", code, body)
	}
}

func TestProjectSbomMissingProject(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/missing/sbom")
	if code != 404 {
		t.Fatalf("sbom missing = %d body=%s", code, body)
	}
}
