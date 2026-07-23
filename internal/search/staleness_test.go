package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

func TestAnnotateStaleness_freshAndStale(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rel := "hello.go"
	content := []byte("package hello\n")
	if err := os.WriteFile(filepath.Join(dir, rel), content, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := indexing.ContentHash(content)
	indexedAt := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	st := &fakeStore{
		project: &store.Project{ID: 1, Name: "p", Path: dir, Model: "bge-m3"},
		simResults: []store.SearchResult{
			{FilePath: rel, Content: "package hello", Score: 0.9, StartLine: 1, EndLine: 1},
		},
		fileInfos: map[string]store.FileHashInfo{
			rel: {Hash: hash, IndexedAt: indexedAt},
		},
	}
	emb := &fakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc := NewService(st, emb)

	resp, err := svc.Search(context.Background(), Request{Project: "p", Query: "hello package", TopK: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(resp.Results))
	}
	if resp.Results[0].Stale {
		t.Error("fresh file should not be Stale")
	}
	if !resp.Results[0].IndexedAt.Equal(indexedAt) {
		t.Errorf("IndexedAt = %v, want %v", resp.Results[0].IndexedAt, indexedAt)
	}

	// Mutate the on-disk file → next search must flip Stale.
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("package hello\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp2, err := svc.Search(context.Background(), Request{Project: "p", Query: "hello package", TopK: 3})
	if err != nil {
		t.Fatalf("Search after mutate: %v", err)
	}
	if len(resp2.Results) != 1 || !resp2.Results[0].Stale {
		t.Fatalf("after mutate: stale=%v results=%+v", len(resp2.Results) > 0 && resp2.Results[0].Stale, resp2.Results)
	}
	if !resp2.Results[0].IndexedAt.Equal(indexedAt) {
		t.Errorf("IndexedAt after mutate = %v, want %v", resp2.Results[0].IndexedAt, indexedAt)
	}
}

func TestAnnotateStaleness_noPathLeavesFresh(t *testing.T) {
	t.Parallel()
	st := &fakeStore{
		project: &store.Project{ID: 1, Name: "p", Path: "", Model: "bge-m3"},
		simResults: []store.SearchResult{
			{FilePath: "a.go", Content: "x", Score: 0.9},
		},
		fileInfos: map[string]store.FileHashInfo{
			"a.go": {Hash: "deadbeef", IndexedAt: time.Now()},
		},
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1}, dims: 1})
	resp, err := svc.Search(context.Background(), Request{Project: "p", Query: "natural language query about a"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Results[0].Stale {
		t.Error("missing project path must not mark stale")
	}
}

func TestAnnotateStaleness_listErrorIsBestEffort(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := &fakeStore{
		project: &store.Project{ID: 1, Name: "p", Path: dir, Model: "bge-m3"},
		simResults: []store.SearchResult{
			{FilePath: "a.go", Content: "x", Score: 0.9},
		},
		fileInfosErr: context.Canceled,
	}
	svc := NewService(st, &fakeEmbedder{vec: []float32{1}, dims: 1})
	resp, err := svc.Search(context.Background(), Request{Project: "p", Query: "natural language query about a"})
	if err != nil {
		t.Fatalf("Search should not fail on staleness error: %v", err)
	}
	if resp.Results[0].Stale {
		t.Error("list error must leave Stale=false")
	}
}

func TestHashProjectFile_rejectsTraversal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := hashProjectFile(dir, "../outside.go"); got != "" {
		t.Errorf("traversal hash = %q, want empty", got)
	}
	if got := hashProjectFile(dir, "missing.go"); got != "" {
		t.Errorf("missing file hash = %q, want empty", got)
	}
}

func TestContentHashMatchesIndexer(t *testing.T) {
	t.Parallel()
	data := []byte("func main() {}\n")
	if indexing.ContentHash(data) == "" {
		t.Fatal("ContentHash empty")
	}
	// Stable known vector: sha256 of empty is fixed.
	empty := indexing.ContentHash(nil)
	if len(empty) != 64 {
		t.Fatalf("hex length = %d, want 64", len(empty))
	}
}
