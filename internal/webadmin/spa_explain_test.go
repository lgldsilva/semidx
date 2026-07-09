package webadmin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestExplainFromIndexAPI(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	fs.chunks = []store.SearchResult{{Content: "package main\n\nfunc main() {\n}\n", StartLine: 1, EndLine: 4}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain?path=main.go&line=3")
	if code != 200 || !strings.Contains(body, `"source":"index"`) {
		t.Fatalf("explain index = %d body=%s", code, body)
	}
}

func TestExplainFromDiskAPI(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Path: root}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, body := getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain?path=main.go&line=3")
	if code != 200 || !strings.Contains(body, "Hello") || !strings.Contains(body, `"source":"disk"`) {
		t.Fatalf("explain disk = %d body=%s", code, body)
	}
}

func TestExplainValidation(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo"}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	code, _ := getBody(t, c, srv.URL+"/admin/api/projects/demo/analyze/explain?path=main.go")
	if code != 400 {
		t.Fatalf("missing line = %d", code)
	}
}

func TestSymbolAtLine(t *testing.T) {
	syms := []analyzer.Symbol{{Name: "Foo", Kind: "func", StartLine: 1, EndLine: 5}}
	if got := symbolAtLine(syms, 3); got == nil || got.Name != "Foo" {
		t.Fatalf("symbolAtLine = %#v", got)
	}
}

func TestGoPackageName(t *testing.T) {
	if got := goPackageName([]byte("package demo\n")); got != "demo" {
		t.Fatalf("pkg=%q", got)
	}
}

func TestExtBreakdown(t *testing.T) {
	got := extBreakdown(map[string]string{"a.go": "1", "b.md": "2"})
	if got["go"] != 1 || got["md"] != 1 {
		t.Fatalf("breakdown=%v", got)
	}
}
