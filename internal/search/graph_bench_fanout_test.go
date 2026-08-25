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
)

// newBenchStore opens an empty SQLite index for a benchmark fixture.
func newBenchStore(b *testing.B, ctx context.Context) (*localstore.SQLiteStore, int) {
	b.Helper()
	st, err := localstore.New(filepath.Join(b.TempDir(), "bench.db"))
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
	return st, pid
}

// insertBenchFile indexes one text-only file with a single chunk.
func insertBenchFile(b *testing.B, ctx context.Context, st *localstore.SQLiteStore, pid int, relPath, content string) {
	b.Helper()
	h := sha256.Sum256([]byte(content))
	fid, err := st.UpsertFile(ctx, pid, relPath, fmt.Sprintf("%x", h[:]), len(content))
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
}

// buildFanOutFixture creates a hub-and-spoke dependency graph: one entry file
// importing numSpokes others. The existing fixtures are chains, so BFS reaches
// only a handful of files per hop and never approaches the 100-path expansion
// cap. Real repositories look more like this — a package imported everywhere —
// and it is where the per-path chunk fetch cost actually shows up.
func buildFanOutFixture(b *testing.B, numSpokes int) (*localstore.SQLiteStore, func()) {
	b.Helper()
	ctx := context.Background()

	st, pid := newBenchStore(b, ctx)
	addFile := func(relPath, content string) {
		insertBenchFile(b, ctx, st, pid, relPath, content)
	}

	// Only the hub shares vocabulary with the query; every spoke is reachable
	// through graph expansion alone.
	addFile("main.go", "package main\n// entry point handles incoming request\nfunc main() {}")

	spokes := make([]string, 0, numSpokes)
	for i := range numSpokes {
		relPath := fmt.Sprintf("pkg%d/a%d.go", i, i)
		vocab := vocabularies[(i+1)%len(vocabularies)]
		addFile(relPath, fmt.Sprintf("package pkg%d\n// %s\nfunc Proc%d() {}", i, vocab, i))
		spokes = append(spokes, relPath)
	}
	if err := st.InsertFileDependencies(ctx, pid, "main.go", spokes); err != nil {
		b.Fatalf("InsertFileDependencies: %v", err)
	}

	return st, func() { st.Close() }
}

func benchFanOut(b *testing.B, numSpokes int) {
	b.ReportAllocs()
	st, cleanup := buildFanOutFixture(b, numSpokes)
	defer cleanup()

	svc := NewService(st, nil)
	svc.emb = &benchFakeEmbedder{vec: []float32{1, 2, 3}, dims: 3}
	ctx := context.Background()

	b.ResetTimer()
	for b.Loop() {
		_, _ = svc.Search(ctx, Request{
			Project: "bench", Query: "entry point handles request",
			TopK: 10, KeywordOnly: true, Graph: true, GraphMaxDepth: 2,
		})
	}
}

// BenchmarkGraphRAG_FanOut150 saturates the 100-path expansion cap, so the
// expansion pays one chunk fetch per discovered file.
func BenchmarkGraphRAG_FanOut150(b *testing.B) { benchFanOut(b, 150) }

// BenchmarkGraphRAG_FanOut40 stays under the cap — the common case for a
// moderately connected file.
func BenchmarkGraphRAG_FanOut40(b *testing.B) { benchFanOut(b, 40) }
