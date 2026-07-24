package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestGraphSubgraph(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.Method != "GET" || r.URL.Path != "/api/v1/projects/proj/graph/subgraph" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("seed") != "main.go" || q.Get("depth") != "3" || q.Get("limit") != "50" {
			t.Errorf("query = %v", q)
		}
		_ = json.NewEncoder(w).Encode(GraphSubgraphResponse{
			Nodes:     []GraphNode{{ID: "main.go", Label: "main.go", Kind: "file", Seed: true}},
			Edges:     []GraphEdge{{Source: "main.go", Target: "pkg/", Kind: "imports"}},
			Truncated: true,
		})
	})
	defer done()

	sg, err := c.GraphSubgraph(context.Background(), "proj", "main.go", 3, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(sg.Nodes) != 1 || !sg.Nodes[0].Seed || len(sg.Edges) != 1 || !sg.Truncated {
		t.Errorf("subgraph = %+v", sg)
	}
}

// TestGraphSubgraphOmitsUnsetBudgets: a zero depth/limit must not be sent, so
// the server's own defaults apply instead of "0".
func TestGraphSubgraphOmitsUnsetBudgets(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string, got %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(GraphSubgraphResponse{})
	})
	defer done()

	if _, err := c.GraphSubgraph(context.Background(), "proj", "", 0, 0); err != nil {
		t.Fatal(err)
	}
}

func TestGraphPath(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.Method != "GET" || r.URL.Path != "/api/v1/projects/proj/graph/path" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("from") != "a.go" || q.Get("to") != "b.go" ||
			q.Get("max_depth") != "4" || q.Get("undirected") != "1" {
			t.Errorf("query = %v", q)
		}
		_ = json.NewEncoder(w).Encode(GraphPathResponse{
			From: "a.go", To: "b.go", Found: true, Directed: false,
			Hops: []string{"a.go", "pkg/", "b.go"}, Length: 2,
		})
	})
	defer done()

	pr, err := c.GraphPath(context.Background(), "proj", "a.go", "b.go", 4, true)
	if err != nil {
		t.Fatal(err)
	}
	if !pr.Found || pr.Directed || pr.Length != 2 || len(pr.Hops) != 3 {
		t.Errorf("path = %+v", pr)
	}
}

// TestGraphMethodsRequireProject keeps the empty-project guard: without it the
// request collapses to /api/v1/projects/graph/path and the server 405s.
func TestGraphMethodsRequireProject(t *testing.T) {
	c := New("http://example.invalid", "tok")
	if _, err := c.GraphSubgraph(context.Background(), "", "a.go", 0, 0); err == nil {
		t.Error("GraphSubgraph with an empty project should error")
	}
	if _, err := c.GraphPath(context.Background(), "  ", "a.go", "b.go", 0, false); err == nil {
		t.Error("GraphPath with a blank project should error")
	}
}

// TestGraphPathEscapesProjectName: a project name with a slash must stay one
// path segment, so it cannot be read as a different route.
func TestGraphPathEscapesProjectName(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/projects/acme%2Fapp/graph/path" {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		_ = json.NewEncoder(w).Encode(GraphPathResponse{})
	})
	defer done()

	if _, err := c.GraphPath(context.Background(), "acme/app", "a.go", "b.go", 0, false); err != nil {
		t.Fatal(err)
	}
}
