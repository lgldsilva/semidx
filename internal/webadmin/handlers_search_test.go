package webadmin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

type offlineEmbedder struct{ fakeEmbedder }

func (offlineEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, errors.New("offline")
}

// degradedEmbedder simulates an open embedding circuit: query embedding fails
// with a RetryableError, so searches must degrade to keyword results.
type degradedEmbedder struct{ fakeEmbedder }

func (degradedEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, &embed.RetryableError{Err: errors.New("circuit open"), After: 2 * time.Second}
}

func TestParseSearchData(t *testing.T) {
	r := httptest.NewRequest("GET", "/admin/search?q=hello&project=jackui&top=25", nil)
	d, topK := parseSearchData(r)
	if topK != 25 || d.Query != "hello" || d.Project != "jackui" || d.AllProjects {
		t.Fatalf("parseSearchData = %+v topK=%d", d, topK)
	}

	r2 := httptest.NewRequest("GET", "/admin/search?all=1&q=hi&top=999", nil)
	d2, topK2 := parseSearchData(r2)
	if topK2 != 10 || !d2.AllProjects || d2.Project != "" {
		t.Fatalf("all+invalid top = %+v topK=%d", d2, topK2)
	}
}

func TestMergeProjectSearchesUsesIdentity(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{{
		ID: 1, Name: "display", Identity: "git:example/app", Model: "bge-m3",
	}}
	fs.searchResults = []store.SearchResult{{FilePath: "main.go", Content: "x", Score: 0.9}}
	a, err := New(fs, fakeEmbedder{}, nil, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	merged, flags, err := a.mergeProjectSearches(context.Background(), fs.projects, "q", 5)
	if err != nil || flags.Fallback || len(merged) != 1 || merged[0].FilePath != "main.go" {
		t.Fatalf("merge = %+v flags=%+v err=%v", merged, flags, err)
	}
}

func TestMergeProjectSearchesSkipsMissing(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{
		{ID: 1, Name: "alpha", Identity: "git:ok", Model: "bge-m3"},
		{ID: 2, Name: "stale", Identity: "git:gone", Model: "bge-m3"},
	}
	fs.hideIdentities = map[string]struct{}{"git:gone": {}}
	fs.searchResults = []store.SearchResult{{FilePath: "main.go", Content: "x", Score: 0.9}}
	a, err := New(fs, fakeEmbedder{}, nil, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	merged, _, err := a.mergeProjectSearches(context.Background(), fs.projects, "q", 5)
	if err != nil || len(merged) != 1 {
		t.Fatalf("merge skip missing = %+v err=%v", merged, err)
	}
}

func TestMergeProjectSearchesTruncatesTopK(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{
		{ID: 1, Name: "a", Model: "bge-m3"},
		{ID: 2, Name: "b", Model: "bge-m3"},
	}
	fs.searchResults = []store.SearchResult{
		{FilePath: "low.go", Content: "x", Score: 0.5},
		{FilePath: "high.go", Content: "y", Score: 0.95},
	}
	a, err := New(fs, fakeEmbedder{}, nil, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	merged, _, err := a.mergeProjectSearches(context.Background(), fs.projects, "q", 1)
	if err != nil || len(merged) != 1 || merged[0].FilePath != "high.go" {
		t.Fatalf("truncated merge = %+v err=%v", merged, err)
	}
}

func TestMergeProjectSearchesSetsFallbackFlag(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{{ID: 1, Name: "alpha", Model: "bge-m3"}}
	fs.searchResults = []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.5}}
	a, err := New(fs, offlineEmbedder{}, nil, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	merged, flags, err := a.mergeProjectSearches(context.Background(), fs.projects, "offline embed query", 5)
	if err != nil || !flags.Fallback || len(merged) != 1 {
		t.Fatalf("merge fallback = %+v flags=%+v err=%v", merged, flags, err)
	}
	if flags.Degraded {
		t.Fatal("a plain (non-retryable) embed failure must not flag Degraded")
	}
}

func TestMergeProjectSearchesSetsDegradedFlag(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{{ID: 1, Name: "alpha", Model: "bge-m3"}}
	fs.searchResults = []store.SearchResult{{FilePath: "a.go", Content: "x", Score: 0.5}}
	a, err := New(fs, degradedEmbedder{}, nil, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	merged, flags, err := a.mergeProjectSearches(context.Background(), fs.projects, "degraded embed query", 5)
	if err != nil || len(merged) != 1 {
		t.Fatalf("merge degraded = %+v err=%v", merged, err)
	}
	if !flags.Degraded || !flags.Fallback || flags.RetryAfter != 2*time.Second {
		t.Fatalf("flags = %+v, want degraded+fallback with 2s hint", flags)
	}
}

func TestMergeProjectSearchesPropagatesError(t *testing.T) {
	fs := newFakeStore()
	fs.projects = []store.Project{{ID: 1, Name: "alpha", Model: "bge-m3"}}
	fs.searchErr = context.Canceled
	a, err := New(fs, fakeEmbedder{}, nil, false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.mergeProjectSearches(context.Background(), fs.projects, "q", 5); err == nil {
		t.Fatal("expected search error")
	}
}

// TestAPISearchDegraded: when the embedding circuit is open, /admin/api/search
// answers 200 with keyword results plus degraded=true and a retry_after_ms
// hint, both for a single project and for all=true.
func TestAPISearchDegraded(t *testing.T) {
	srv, fs := newAdminWith(t, degradedEmbedder{}, nil)
	fs.addUser("admin", "supersecret", "admin")
	fs.projects = []store.Project{{ID: 1, Name: "demo", Model: "bge-m3", Dims: 3}}
	fs.searchProject = &store.Project{ID: 1, Name: "demo", Model: "bge-m3", Dims: 3}
	fs.searchResults = []store.SearchResult{{FilePath: "a.go", StartLine: 1, EndLine: 2, Content: "func main", Score: 0.5}}
	c := newClient(t, srv)
	login(t, c, srv.URL, "admin", "supersecret")
	csrf := csrfFrom(t, c, srv.URL+"/admin/keys")

	code, body := postAdminJSON(t, c, srv.URL+"/admin/api/search", csrf, map[string]any{
		"query": "main function", "project": "demo", "top": 5,
	})
	if code != http.StatusOK {
		t.Fatalf("degraded search = %d body=%s (want 200)", code, body)
	}
	if !strings.Contains(body, `"degraded":true`) || !strings.Contains(body, `"retry_after_ms":2000`) {
		t.Fatalf("degraded search body missing flags: %s", body)
	}
	if !strings.Contains(body, `"a.go"`) {
		t.Fatalf("degraded search body missing keyword results: %s", body)
	}

	code, body = postAdminJSON(t, c, srv.URL+"/admin/api/search", csrf, map[string]any{
		"query": "main function", "all": true, "top": 5,
	})
	if code != http.StatusOK {
		t.Fatalf("degraded all-projects search = %d body=%s (want 200)", code, body)
	}
	if !strings.Contains(body, `"degraded":true`) || !strings.Contains(body, `"retry_after_ms":2000`) {
		t.Fatalf("degraded all-projects body missing flags: %s", body)
	}
}
