package webadmin

import (
	"net/http"
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

// newDepGraphAdmin wires an admin whose project has main.go importing pkg/,
// with pkg/util.go in the file inventory — the minimum needed for a two-file
// path to exist through the synthetic package hop.
func newDepGraphAdmin(t *testing.T) (string, *http.Client) {
	t.Helper()
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3"}}
	fs.graph = map[string][]string{"main.go": {"pkg/"}}
	fs.fileHashes = map[string]string{"main.go": "h1", "pkg/util.go": "h2"}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	return srv.URL, c
}

func TestGraphSubgraphAPI(t *testing.T) {
	base, c := newDepGraphAdmin(t)

	code, body := getBody(t, c, base+"/admin/api/projects/demo/graph/subgraph?seed=main.go&depth=2")
	if code != 200 {
		t.Fatalf("subgraph = %d body=%s", code, body)
	}
	for _, want := range []string{`"nodes"`, `"edges"`, `"main.go"`, `"pkg/"`, `"imports"`} {
		if !strings.Contains(body, want) {
			t.Errorf("subgraph body missing %q:\n%s", want, body)
		}
	}
}

func TestGraphPathAPI(t *testing.T) {
	base, c := newDepGraphAdmin(t)

	code, body := getBody(t, c, base+"/admin/api/projects/demo/graph/path?from=main.go&to=pkg/util.go")
	if code != 200 || !strings.Contains(body, `"found":true`) {
		t.Fatalf("path = %d body=%s", code, body)
	}
	if !strings.Contains(body, `"directed":true`) {
		t.Errorf("expected a directed path:\n%s", body)
	}

	// Missing endpoints are a client error, not an empty result.
	code, body = getBody(t, c, base+"/admin/api/projects/demo/graph/path?from=main.go")
	if code != 400 || !strings.Contains(body, "from and to are required") {
		t.Fatalf("path missing to = %d body=%s", code, body)
	}
}

func TestGraphSubgraphProjectNotFound(t *testing.T) {
	srv, fs := newAdminWith(t, fakeEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")

	if code, _ := getBody(t, c, srv.URL+"/admin/api/projects/ghost/graph/subgraph"); code != 404 {
		t.Errorf("subgraph missing project = %d, want 404", code)
	}
	if code, _ := getBody(t, c, srv.URL+"/admin/api/projects/ghost/graph/path?from=a&to=b"); code != 404 {
		t.Errorf("path missing project = %d, want 404", code)
	}
}

// TestGraphQueryBudgetsAreClamped guards the DoS budget: an absurd depth must be
// capped rather than accepted, and garbage must fall back to the default.
func TestGraphQueryBudgetsAreClamped(t *testing.T) {
	if got := clampAdminQueryInt("9999", maxAdminSubgraphDepth); got != maxAdminSubgraphDepth {
		t.Errorf("depth 9999 clamped to %d, want %d", got, maxAdminSubgraphDepth)
	}
	for _, raw := range []string{"", "abc", "-3"} {
		if got := clampAdminQueryInt(raw, maxAdminPathDepth); got != 0 {
			t.Errorf("clampAdminQueryInt(%q) = %d, want 0", raw, got)
		}
	}
	if got := clampAdminQueryInt("3", maxAdminSubgraphDepth); got != 3 {
		t.Errorf("in-range depth = %d, want 3", got)
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
