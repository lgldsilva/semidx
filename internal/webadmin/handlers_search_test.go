package webadmin

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

type offlineEmbedder struct{ fakeEmbedder }

func (offlineEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, errors.New("offline")
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
	merged, fallback, err := a.mergeProjectSearches(context.Background(), fs.projects, "q", 5)
	if err != nil || fallback || len(merged) != 1 || merged[0].FilePath != "main.go" {
		t.Fatalf("merge = %+v fallback=%v err=%v", merged, fallback, err)
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
	merged, fallback, err := a.mergeProjectSearches(context.Background(), fs.projects, "q", 5)
	if err != nil || !fallback || len(merged) != 1 {
		t.Fatalf("merge fallback = %+v flag=%v err=%v", merged, fallback, err)
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
