package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pgregory.net/rapid"
)

// TestProjectNameIsPathEscaped is a property check: for any project name, the
// client sends it as a single, correctly percent-escaped path segment, and a
// server using the same {project} routing recovers the exact original name.
func TestProjectNameIsPathEscaped(t *testing.T) {
	var gotName string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/projects/{project}", func(w http.ResponseWriter, r *http.Request) {
		gotName = r.PathValue("project")
		_ = json.NewEncoder(w).Encode(Project{Name: gotName})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "tok")

	// A rune set of names that survive as a single path segment (no '/').
	runes := []rune("abcXYZ012 ._-%&=+,éçü#")
	rapid.Check(t, func(rt *rapid.T) {
		name := string(rapid.SliceOfN(rapid.SampledFrom(runes), 1, 24).Draw(rt, "name"))
		// "." and ".." are collapsed by HTTP path normalization on both ends, so
		// they can never survive as a project segment — a routing fact, not a
		// client-side escaping concern. Nudge them to a normal single segment.
		if name == "." || name == ".." {
			name += "-"
		}
		p, err := c.GetProject(context.Background(), name)
		if err != nil {
			rt.Fatalf("GetProject(%q) error: %v", name, err)
		}
		if gotName != name {
			rt.Fatalf("server received project %q, want %q (escaping is wrong)", gotName, name)
		}
		if p.Name != name {
			rt.Fatalf("round-tripped name = %q, want %q", p.Name, name)
		}
	})
}

func TestWithHTTPClientOption(t *testing.T) {
	custom := &http.Client{}
	c := New("https://example.test/", "tok", WithHTTPClient(custom))
	if c.http != custom {
		t.Error("WithHTTPClient did not override the default http.Client")
	}
	if c.baseURL != "https://example.test" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
}

func TestAPIErrorString(t *testing.T) {
	withMsg := &APIError{Status: 404, Message: "project not found"}
	if got := withMsg.Error(); got != "semidx: 404: project not found" {
		t.Errorf("Error() = %q", got)
	}
	noMsg := &APIError{Status: 500}
	if got := noMsg.Error(); got != "semidx: unexpected status 500" {
		t.Errorf("Error() = %q", got)
	}
}

func TestHealthz(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		c, done := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				t.Errorf("path = %s", r.URL.Path)
			}
			_, _ = w.Write([]byte("ok"))
		})
		defer done()
		if err := c.Healthz(context.Background()); err != nil {
			t.Errorf("Healthz = %v, want nil", err)
		}
	})

	t.Run("server down", func(t *testing.T) {
		c, done := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(503)
		})
		defer done()
		if err := c.Healthz(context.Background()); err == nil {
			t.Error("Healthz should error on 503")
		}
	})
}

// TestErrorPropagation drives every method against a server that always fails,
// covering each method's error-return branch.
func TestErrorPropagation(t *testing.T) {
	c, done := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	})
	defer done()
	ctx := context.Background()

	if _, err := c.Search(ctx, "p", "q", "", 5, false, 0); err == nil {
		t.Error("Search should propagate a 500")
	}
	if _, err := c.ListProjects(ctx); err == nil {
		t.Error("ListProjects should propagate a 500")
	}
	if _, err := c.GetProject(ctx, "p"); err == nil {
		t.Error("GetProject should propagate a 500")
	}
	if _, err := c.CreateProject(ctx, "p", "m", "push", "", ""); err == nil {
		t.Error("CreateProject should propagate a 500")
	}
	if err := c.DeleteProject(ctx, "p"); err == nil {
		t.Error("DeleteProject should propagate a 500")
	}
	if _, err := c.EnqueueJob(ctx, "p", "full"); err == nil {
		t.Error("EnqueueJob should propagate a 500")
	}
	if _, err := c.GetJob(ctx, "p", 1); err == nil {
		t.Error("GetJob should propagate a 500")
	}
	if _, err := c.FilesDiff(ctx, "p", map[string]string{"a": "b"}); err == nil {
		t.Error("FilesDiff should propagate a 500")
	}
	if _, err := c.FilesBatch(ctx, "p", []BatchFile{{Path: "a"}}, nil); err == nil {
		t.Error("FilesBatch should propagate a 500")
	}
}

func TestDoTransportError(t *testing.T) {
	// Point at a server that is closed immediately → the round-trip fails.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	c := New(url, "tok")
	if err := c.Healthz(context.Background()); err == nil {
		t.Error("expected a transport error against a closed server")
	}
}

func TestDoRequestBuildError(t *testing.T) {
	// A nil context makes http.NewRequestWithContext fail.
	c := New("http://example.test", "tok")
	//nolint:staticcheck // deliberately passing a nil context to hit the error path
	if err := c.do(nil, http.MethodGet, "/healthz", nil, nil); err == nil {
		t.Error("expected an error building a request with a nil context")
	}
}

func TestDoMarshalError(t *testing.T) {
	c := New("http://example.test", "tok")
	// A channel cannot be JSON-marshalled → do returns the marshal error before
	// any network call.
	if err := c.do(context.Background(), http.MethodPost, "/x", make(chan int), nil); err == nil {
		t.Error("expected a JSON marshal error for an unmarshalable body")
	}
}
