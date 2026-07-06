package search

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/localstore"
	"github.com/lgldsilva/semidx/internal/store"
)

// vocabularies defines deliberately isolated vocabularies for each depth level.
// Only file 0 (main.go) shares words with the query. Files 1+ use different
// vocabularies so keyword search cannot find them — only graph expansion can.
var vocabularies = []string{
	"entry point handles incoming request",                                     // file 0: shares words with query
	"dispatcher routes data packets",                                           // file 1: completely different words
	"worker consumes queue elements",                                          // file 2
	"backend persists database records",                                       // file 3
	"cache stores session blobs",                                              // file 4
}

// buildBenchFixture creates a temp SQLite store with dep chain + pre-populated edges.
func buildBenchFixture(b *testing.B, numFiles int) (*localstore.SQLiteStore, int, map[string]bool, func()) {
	b.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(b.TempDir(), "bench.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		b.Fatalf("localstore.New: %v", err)
	}

	if err := st.EnsureChunksTable(ctx, 1); err != nil {
		b.Fatalf("EnsureChunksTable: %v", err)
	}

	pid, err := st.UpsertProject(ctx, "bench", "/tmp/bench", "keyword", 0)
	if err != nil {
		b.Fatalf("UpsertProject: %v", err)
	}

	relevant := make(map[string]bool)
	prevFile := ""

	for i := range numFiles {
		relPath := fmt.Sprintf("pkg%d/a%d.go", i, i)
		if i == 0 {
			relPath = "main.go"
		}

		vocab := vocabularies[i%len(vocabularies)]
		content := fmt.Sprintf("package pkg%d\n// %s\nfunc Proc%d() {}", i, vocab, i)

		h := sha256.Sum256([]byte(content))
		hash := fmt.Sprintf("%x", h[:])
		fid, err := st.UpsertFile(ctx, pid, relPath, hash, len(content))
		if err != nil {
			b.Fatalf("UpsertFile: %v", err)
		}

		chunks := []chunker.Chunk{{
			Content:   content,
			StartLine: 1,
			EndLine:   strings.Count(content, "\n") + 1,
		}}
		if err := st.InsertChunksTextOnly(ctx, pid, fid, chunks, 1); err != nil {
			b.Fatalf("InsertChunksTextOnly: %v", err)
		}

		if prevFile != "" {
			if err := st.InsertFileDependencies(ctx, pid, prevFile, []string{relPath}); err != nil {
				b.Fatalf("InsertFileDependencies: %v", err)
			}
		}

		// Files 0-2 are relevant (reachable within maxDepth=2 from seed).
		if i <= 2 {
			relevant[relPath] = true
		}
		prevFile = relPath
	}

	return st, pid, relevant, func() { st.Close() }
}

// buildBenchFixtureT is the *testing.T variant.
func buildBenchFixtureT(t *testing.T, numFiles int) (*localstore.SQLiteStore, int, map[string]bool, func()) {
	t.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "bench.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}

	if err := st.EnsureChunksTable(ctx, 1); err != nil {
		t.Fatalf("EnsureChunksTable: %v", err)
	}

	pid, err := st.UpsertProject(ctx, "bench", "/tmp/bench", "keyword", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	relevant := make(map[string]bool)
	prevFile := ""

	for i := range numFiles {
		relPath := fmt.Sprintf("pkg%d/a%d.go", i, i)
		if i == 0 {
			relPath = "main.go"
		}

		vocab := vocabularies[i%len(vocabularies)]
		content := fmt.Sprintf("package pkg%d\n// %s\nfunc Proc%d() {}", i, vocab, i)

		h := sha256.Sum256([]byte(content))
		hash := fmt.Sprintf("%x", h[:])
		fid, err := st.UpsertFile(ctx, pid, relPath, hash, len(content))
		if err != nil {
			t.Fatalf("UpsertFile: %v", err)
		}

		chunks := []chunker.Chunk{{
			Content:   content,
			StartLine: 1,
			EndLine:   strings.Count(content, "\n") + 1,
		}}
		if err := st.InsertChunksTextOnly(ctx, pid, fid, chunks, 1); err != nil {
			t.Fatalf("InsertChunksTextOnly: %v", err)
		}

		if prevFile != "" {
			if err := st.InsertFileDependencies(ctx, pid, prevFile, []string{relPath}); err != nil {
				t.Fatalf("InsertFileDependencies: %v", err)
			}
		}

		if i <= 2 {
			relevant[relPath] = true
		}
		prevFile = relPath
	}

	return st, pid, relevant, func() { st.Close() }
}

// TestGraphRAGRealCompare validates recall gain with real SQLite store
// using vocabulary-isolated files.
func TestGraphRAGRealCompare(t *testing.T) {
	ctx := context.Background()
	st, _, relevant, cleanup := buildBenchFixtureT(t, 5)
	defer cleanup()

	svc := NewService(st, nil)
	fakeEmb := &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	svc.emb = fakeEmb

	// Query uses words from main.go's vocabulary ("entry point handles incoming request").
	query := "entry point handles request"

	// Baseline: no graph.
	respNo, err := svc.Search(ctx, Request{
		Project: "bench", Query: query, TopK: 5, KeywordOnly: true, Graph: false,
	})
	if err != nil {
		t.Fatalf("Search (no graph): %v", err)
	}
	baselineFound := countRelevantFiles(respNo.Results, relevant)
	baselineRecall := float64(baselineFound) / float64(len(relevant))

	// Graph-enabled.
	respGraph, err := svc.Search(ctx, Request{
		Project: "bench", Query: query, TopK: 5, KeywordOnly: true,
		Graph: true, GraphMaxDepth: 2,
	})
	if err != nil {
		t.Fatalf("Search (graph): %v", err)
	}
	graphFound := countRelevantFiles(respGraph.Results, relevant)
	graphRecall := float64(graphFound) / float64(len(relevant))

	t.Logf("========== COMPARATIVE (real SQLite, vocab-isolated) ==========")
	t.Logf("  Fixture: 5 files, main → pkg0 → pkg1 → pkg2 → pkg3")
	t.Logf("  Query:   %q", query)
	t.Logf("  Relevant: %d files (main, pkg0, pkg1 — reachable within 2 hops)", len(relevant))
	t.Logf("")
	t.Logf("  WITHOUT GRAPH:  %d results, %d/%d relevant, recall=%.0f%%",
		len(respNo.Results), baselineFound, len(relevant), baselineRecall*100)
	t.Logf("  WITH GRAPH:     %d results, %d/%d relevant, recall=%.0f%%",
		len(respGraph.Results), graphFound, len(relevant), graphRecall*100)

	delta := (graphRecall - baselineRecall) * 100
	if delta > 0 {
		t.Logf("  RECALL GAIN: +%.0fpp", delta)
	} else {
		t.Errorf("  NO RECALL GAIN: delta=%.0fpp — graph expansion failed to improve recall", delta)
	}

	if graphRecall <= baselineRecall {
		t.Errorf("FAIL: graph expansion did not improve recall (baseline=%.0f%%, graph=%.0f%%)",
			baselineRecall*100, graphRecall*100)
	}
}

// BenchmarkGraphRAG_RealDisabled — real SQLite, keyword search, no graph.
func BenchmarkGraphRAG_RealDisabled(b *testing.B) {
	b.ReportAllocs()
	st, _, _, cleanup := buildBenchFixture(b, 5)
	defer cleanup()

	svc := NewService(st, nil)
	svc.emb = &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, Request{
			Project: "bench", Query: "entry point handles request",
			TopK: 5, KeywordOnly: true, Graph: false,
		})
	}
}

// BenchmarkGraphRAG_RealEnabled — real SQLite, keyword search, with graph.
func BenchmarkGraphRAG_RealEnabled(b *testing.B) {
	b.ReportAllocs()
	st, _, _, cleanup := buildBenchFixture(b, 5)
	defer cleanup()

	svc := NewService(st, nil)
	svc.emb = &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, Request{
			Project: "bench", Query: "entry point handles request",
			TopK: 5, KeywordOnly: true, Graph: true, GraphMaxDepth: 2,
		})
	}
}

// BenchmarkGraphRAG_RealScaled — 20-file chain, with graph.
func BenchmarkGraphRAG_RealScaled(b *testing.B) {
	b.ReportAllocs()
	st, _, _, cleanup := buildBenchFixture(b, 20)
	defer cleanup()

	svc := NewService(st, nil)
	svc.emb = &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, Request{
			Project: "bench", Query: "entry point handles request",
			TopK: 10, KeywordOnly: true, Graph: true, GraphMaxDepth: 3,
		})
	}
}

func countRelevantFiles(results []store.SearchResult, relevant map[string]bool) int {
	count := 0
	seen := make(map[string]bool)
	for _, r := range results {
		if !seen[r.FilePath] && relevant[r.FilePath] {
			count++
			seen[r.FilePath] = true
		}
	}
	return count
}
