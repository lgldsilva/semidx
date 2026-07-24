package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func TestGraphSubgraphAndPath(t *testing.T) {
	fs := &fakeStore{
		token:   &store.Token{Scopes: []string{"read"}},
		project: &store.Project{ID: 1, Name: "demo", Status: "ready", Model: "m"},
		graph: map[string][]string{
			"main.go": {"pkg/util/"},
		},
		fileHashes: map[string]string{
			"main.go":          "h1",
			"pkg/util/help.go": "h2",
		},
	}
	srv := New(fs, fakeEmbedder{}, nil)

	rec := do(t, srv, "GET", "/api/v1/projects/demo/graph/subgraph?seed=main.go&depth=2", "tok", "")
	if rec.Code != 200 {
		t.Fatalf("subgraph status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	nodes, _ := body["nodes"].([]any)
	if len(nodes) < 2 {
		t.Fatalf("nodes=%v", body["nodes"])
	}

	rec = do(t, srv, "GET", "/api/v1/projects/demo/graph/path?from=main.go&to=pkg/util/help.go", "tok", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"found":true`) {
		t.Fatalf("path = %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, srv, "GET", "/api/v1/projects/demo/graph/path?from=main.go", "tok", "")
	if rec.Code != 400 {
		t.Fatalf("missing to = %d", rec.Code)
	}

	rec = do(t, srv, "GET", "/api/v1/projects/demo/graph/path?from=a&to=b", "", "")
	if rec.Code != 401 {
		t.Fatalf("unauth = %d", rec.Code)
	}

	fs.project = nil
	rec = do(t, srv, "GET", "/api/v1/projects/ghost/graph/subgraph", "tok", "")
	if rec.Code != 404 {
		t.Fatalf("missing project = %d", rec.Code)
	}
}

func TestGraphLoadErrors(t *testing.T) {
	fs := &fakeStore{
		token:    &store.Token{Scopes: []string{"read"}},
		project:  &store.Project{ID: 1, Name: "demo", Status: "ready"},
		graphErr: errors.New("boom"),
	}
	srv := New(fs, fakeEmbedder{}, nil)
	rec := do(t, srv, "GET", "/api/v1/projects/demo/graph/subgraph", "tok", "")
	if rec.Code != 500 {
		t.Fatalf("status=%d", rec.Code)
	}
}
