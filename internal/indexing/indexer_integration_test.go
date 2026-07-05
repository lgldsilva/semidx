package indexing

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/localstore"
)

// semanticEmbedder is a deterministic Embedder that maps a chunk to a fixed
// basis vector by keyword, so similarity search has a well-defined, assertable
// ranking. It also counts Embed calls to prove incremental indexing skips work.
type semanticEmbedder struct {
	mu         sync.Mutex
	embedCalls int
}

func (s *semanticEmbedder) basis(text string) []float32 {
	switch {
	case strings.Contains(text, "alpha"):
		return []float32{1, 0, 0}
	case strings.Contains(text, "beta"):
		return []float32{0, 1, 0}
	default:
		return []float32{0, 0, 1}
	}
}

func (s *semanticEmbedder) ModelInfo(ctx context.Context, model string) (*embed.ModelInfo, error) {
	return &embed.ModelInfo{Name: model, Dims: 3}, nil
}
func (s *semanticEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	s.mu.Lock()
	s.embedCalls += len(inputs)
	s.mu.Unlock()
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		out[i] = s.basis(in)
	}
	return out, nil
}
func (s *semanticEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	return s.basis(text), nil
}
func (s *semanticEmbedder) ListModels(ctx context.Context) ([]string, error) {
	return []string{"m"}, nil
}
func (s *semanticEmbedder) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.embedCalls
}

// TestPipelineIndexThenSearchLocalStore is a real end-to-end test: index a
// project into an actual on-disk SQLite store, then prove both vector and
// keyword search recover the indexed content with the correct ranking,
// file paths and line numbers — no fakes on the persistence side.
func TestPipelineIndexThenSearchLocalStore(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	// One chunk per file (no blank lines) with a distinct keyword each.
	writeFile(t, src, "alpha.go", "package a\nfunc Alpha() {} // token alpha here\n")
	writeFile(t, src, "beta.go", "package b\nfunc Beta() {} // token beta here\n")
	writeFile(t, src, "gamma.go", "package g\nfunc Gamma() {} // token gamma here\n")

	pid, err := st.UpsertProject(ctx, "proj", src, "m")
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	emb := &semanticEmbedder{}
	idx := NewIndexer(st, emb, 3, 2, 8, false, false, "")

	stats, err := idx.IndexProject(ctx, pid, src, "m", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if stats.FilesScanned != 3 || stats.FilesIndexed != 3 {
		t.Fatalf("scanned=%d indexed=%d, want 3 each", stats.FilesScanned, stats.FilesIndexed)
	}
	if stats.ChunksCreated != 3 {
		t.Fatalf("ChunksCreated = %d, want 3", stats.ChunksCreated)
	}

	// Vector search aligned with alpha's basis must rank alpha.go first.
	res, err := st.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 3)
	if err != nil {
		t.Fatalf("SearchSimilar: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("SearchSimilar returned %d, want 3", len(res))
	}
	if res[0].FilePath != "alpha.go" {
		t.Errorf("top hit = %q, want alpha.go", res[0].FilePath)
	}
	if res[0].Score < 0.99 {
		t.Errorf("aligned score = %v, want ~1", res[0].Score)
	}
	if res[0].Score < res[1].Score || res[1].Score < res[2].Score {
		t.Errorf("results not sorted by score: %v", []float64{res[0].Score, res[1].Score, res[2].Score})
	}

	// Keyword search recovers a specific function by name, with its line number.
	kw, err := st.SearchSimilarKeywords(ctx, pid, "Gamma", 3, 5)
	if err != nil {
		t.Fatalf("SearchSimilarKeywords: %v", err)
	}
	if len(kw) != 1 || kw[0].FilePath != "gamma.go" {
		t.Fatalf("keyword search = %+v, want the gamma.go chunk", kw)
	}
	if kw[0].StartLine != 1 {
		t.Errorf("keyword hit StartLine = %d, want 1", kw[0].StartLine)
	}
}

// TestPipelineIncrementalIdempotent proves indexing is idempotent against a real
// store: a second run over unchanged content re-embeds nothing (skips every
// file) and the search results are identical.
func TestPipelineIncrementalIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	writeFile(t, src, "alpha.go", "package a\nfunc Alpha() {} // alpha\n")
	writeFile(t, src, "beta.go", "package b\nfunc Beta() {} // beta\n")

	pid, _ := st.UpsertProject(ctx, "proj", src, "m")
	emb := &semanticEmbedder{}
	idx := NewIndexer(st, emb, 3, 2, 8, false, false, "")

	first, err := idx.IndexProject(ctx, pid, src, "m", 0)
	if err != nil {
		t.Fatalf("first IndexProject: %v", err)
	}
	if first.FilesIndexed != 2 {
		t.Fatalf("first run FilesIndexed = %d, want 2", first.FilesIndexed)
	}
	callsAfterFirst := emb.calls()
	if callsAfterFirst == 0 {
		t.Fatal("first run embedded nothing")
	}

	// Second run over identical content: everything is up-to-date and skipped.
	second, err := idx.IndexProject(ctx, pid, src, "m", 0)
	if err != nil {
		t.Fatalf("second IndexProject: %v", err)
	}
	if second.FilesScanned != 2 || second.FilesSkipped != 2 {
		t.Errorf("second run scanned=%d skipped=%d, want 2 skipped", second.FilesScanned, second.FilesSkipped)
	}
	if second.FilesIndexed != 0 {
		t.Errorf("second run FilesIndexed = %d, want 0 (all unchanged)", second.FilesIndexed)
	}
	if emb.calls() != callsAfterFirst {
		t.Errorf("second run re-embedded: calls %d -> %d (want unchanged)", callsAfterFirst, emb.calls())
	}

	// State is stable: search still returns both files.
	res, err := st.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 10)
	if err != nil || len(res) != 2 {
		t.Fatalf("post-idempotency search = %d results, err %v; want 2", len(res), err)
	}
}
