package indexing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStore records how chunks were inserted (embedded vs text-only).
// The store.Store embed satisfies the full IndexStore interface at compile
// time; methods exercised by tests are overridden explicitly below. Untouched
// methods remain nil-interface stubs and must not be called by test paths.
type fakeStore struct {
	store.Store
	mu       sync.Mutex // indexing runs concurrently; guards the fields below
	nextID   int
	embedded []string
	textOnly []string
	status   string
	upToDate bool // FileUpToDate returns this (simulates unchanged files)
}

func (f *fakeStore) FileUpToDate(ctx context.Context, projectID int, path, hash string, dims int) (bool, error) {
	return f.upToDate, nil
}

func (f *fakeStore) UpsertProject(ctx context.Context, name, path, model string, dims int) (int, error) {
	return 1, nil
}
func (f *fakeStore) UpsertFile(ctx context.Context, projectID int, path, hash string, size int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	return f.nextID, nil
}
func (f *fakeStore) DeleteChunksForFile(ctx context.Context, projectID, fileID, dims int) error {
	return nil
}
func (f *fakeStore) InsertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range chunks {
		f.embedded = append(f.embedded, c.Content)
	}
	return nil
}
func (f *fakeStore) InsertChunksTextOnly(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, dims int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range chunks {
		f.textOnly = append(f.textOnly, c.Content)
	}
	return nil
}
func (f *fakeStore) UpdateProjectStatus(ctx context.Context, id int, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
	return nil
}

func (f *fakeStore) InsertFileDependencies(context.Context, int, string, []string) error {
	return nil
}

// fakeEmbedder returns fixed vectors; localAvailable controls whether a
// force-local ModelInfo succeeds (simulating a local provider being present).
type fakeEmbedder struct {
	embed.Embedder
	localAvailable bool
	onEmbed        func() // optional hook invoked on each Embed call
}

func (f *fakeEmbedder) ModelInfo(ctx context.Context, model string) (*embed.ModelInfo, error) {
	if !f.localAvailable {
		return nil, errors.New("no local provider")
	}
	return &embed.ModelInfo{Name: model, Dims: 3}, nil
}
func (f *fakeEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	if f.onEmbed != nil {
		f.onEmbed()
	}
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}
func (f *fakeEmbedder) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	return []float32{1, 0, 0}, nil
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The core privacy guarantee: a sensitive file under a cloud-only model is
// stored text-only (never embedded), while a normal file is embedded. Before
// the fix the sensitive file was silently skipped entirely.
func TestPrivacyRoutingTextOnlyWhenNoLocalProvider(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app/handler.go", "package app\n\nfunc Serve() {}\n")
	writeFile(t, dir, "config/secret.txt", "API_KEY=super-secret-value\n")
	writeFile(t, dir, "node_modules/lib.js", "console.log(1)") // must be skipped

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{localAvailable: false}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	stats, err := idx.IndexProject(context.Background(), 1, dir, "gemini-embedding-2", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	if stats.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2 (node_modules skipped)", stats.FilesScanned)
	}
	if stats.FilesIndexed != 2 {
		t.Errorf("FilesIndexed = %d, want 2", stats.FilesIndexed)
	}
	if fs.status != "ready" {
		t.Errorf("final status = %q, want ready", fs.status)
	}

	embedded := strings.Join(fs.embedded, "\n")
	textOnly := strings.Join(fs.textOnly, "\n")

	if !strings.Contains(embedded, "func Serve") {
		t.Errorf("normal file should be embedded; embedded=%q", embedded)
	}
	if !strings.Contains(textOnly, "API_KEY=super-secret-value") {
		t.Errorf("sensitive file should be stored text-only; textOnly=%q", textOnly)
	}
	if strings.Contains(embedded, "API_KEY") {
		t.Error("SECURITY: sensitive content was sent to the (cloud) embedder")
	}
}

// With a local provider available, a sensitive file is embedded locally rather
// than stored text-only.
func TestPrivacyRoutingEmbedsLocallyWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config/secret.txt", "API_KEY=local-embeds-this\n")

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{localAvailable: true}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	if _, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if len(fs.textOnly) != 0 {
		t.Errorf("nothing should be text-only when a local provider exists; got %v", fs.textOnly)
	}
	if !strings.Contains(strings.Join(fs.embedded, "\n"), "API_KEY=local-embeds-this") {
		t.Error("sensitive file should be embedded locally")
	}
}

// Incremental: when files are already up-to-date, they're skipped (counted as
// FilesSkipped) and never re-embedded.
func TestIncrementalSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	writeFile(t, dir, "b.go", "package b\n\nfunc B() {}\n")

	fs := &fakeStore{upToDate: true}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	stats, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if stats.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2", stats.FilesScanned)
	}
	if stats.FilesSkipped != 2 {
		t.Errorf("FilesSkipped = %d, want 2", stats.FilesSkipped)
	}
	if stats.FilesIndexed != 0 {
		t.Errorf("FilesIndexed = %d, want 0 (all unchanged)", stats.FilesIndexed)
	}
	if len(fs.embedded) != 0 {
		t.Errorf("embedded %d chunks, want 0 (nothing re-embedded)", len(fs.embedded))
	}
}

func TestSkipsEmptyAndCountsChunks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.go", "   \n\t\n")
	writeFile(t, dir, "code.go", "package x\n\nfunc A() {}\n\nfunc B() {}\n")

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	stats, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if stats.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2", stats.FilesScanned)
	}
	if stats.FilesIndexed != 1 {
		t.Errorf("FilesIndexed = %d, want 1 (empty file skipped)", stats.FilesIndexed)
	}
	if stats.ChunksCreated == 0 {
		t.Error("expected chunks from code.go")
	}
}

// A cancelled context stops indexing promptly and returns what was done so far,
// rather than plowing through every file.
func TestIndexProjectStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.go", "b.go", "c.go", "d.go", "e.go"} {
		writeFile(t, dir, n, "package x\n\nfunc F() {}\n")
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel as soon as the first file is embedded; the next loop iteration
	// must see the cancellation and bail out.
	emb := &fakeEmbedder{}
	emb.onEmbed = func() { cancel() }
	idx := NewIndexer(&fakeStore{}, emb, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	stats, err := idx.IndexProject(ctx, 1, dir, "bge-m3", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if stats.FilesScanned != 5 {
		t.Errorf("FilesScanned = %d, want 5", stats.FilesScanned)
	}
	if stats.FilesIndexed >= 5 {
		t.Errorf("FilesIndexed = %d, expected to stop early (<5)", stats.FilesIndexed)
	}
}

func TestSleepBackoffRespectsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := sleepBackoff(ctx, 4); !errors.Is(err, context.Canceled) {
		t.Errorf("sleepBackoff on cancelled ctx = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("sleepBackoff took %v on a cancelled ctx; should return immediately", elapsed)
	}
}

// Many files indexed concurrently: every file is accounted for exactly once and
// there are no data races (run with -race). Guards the worker-pool correctness.
func TestConcurrentIndexingIsComplete(t *testing.T) {
	dir := t.TempDir()
	const n = 50
	for i := 0; i < n; i++ {
		// No blank line → exactly one chunk per file, so embedded count == n.
		writeFile(t, dir, fmt.Sprintf("pkg%d/file%d.go", i, i), fmt.Sprintf("package p%d\nfunc F%d() {}\n", i, i))
	}

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 8, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	stats, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if stats.FilesScanned != n || stats.FilesIndexed != n {
		t.Errorf("scanned=%d indexed=%d, want %d each", stats.FilesScanned, stats.FilesIndexed, n)
	}
	if stats.Errors != 0 {
		t.Errorf("Errors = %d, want 0", stats.Errors)
	}
	if len(fs.embedded) != n { // one chunk per file
		t.Errorf("embedded %d chunks, want %d", len(fs.embedded), n)
	}
}

// The worker pool actually parallelizes: with an embedder that sleeps per call,
// multiple workers run embed calls concurrently (not strictly one-at-a-time).
func TestWorkerPoolParallelizes(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 16; i++ {
		writeFile(t, dir, fmt.Sprintf("f%d.go", i), fmt.Sprintf("package p%d\nfunc F%d() {}\n", i, i))
	}
	var active, peak int32
	emb := &fakeEmbedder{onEmbed: func() {
		cur := atomic.AddInt32(&active, 1)
		defer atomic.AddInt32(&active, -1)
		for {
			old := atomic.LoadInt32(&peak)
			if cur <= old || atomic.CompareAndSwapInt32(&peak, old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}}
	idx := NewIndexer(&fakeStore{}, emb, 3, IndexerOpts{Workers: 8, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	if _, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if peak < 2 {
		t.Errorf("peak concurrent embeds = %d, want >= 2 with 8 workers", peak)
	}
}

// IndexContent (the push path) indexes in-memory content without touching disk.
func TestIndexContent(t *testing.T) {
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	created, err := idx.IndexContent(context.Background(), 1, "x.go", "bge-m3", []byte("package x\nfunc F() {}\n"))
	if err != nil {
		t.Fatalf("IndexContent: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1 chunk", created)
	}
	if len(fs.embedded) != 1 || !strings.Contains(fs.embedded[0], "func F") {
		t.Errorf("embedded = %v", fs.embedded)
	}

	// Empty content is a no-op.
	if c, _ := idx.IndexContent(context.Background(), 1, "empty.go", "bge-m3", []byte("  \n\t")); c != 0 {
		t.Errorf("empty content created = %d, want 0", c)
	}
}

func TestScanFilesRespectsMaxAndIgnores(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "x")
	writeFile(t, dir, "b.go", "y")
	writeFile(t, dir, "c.go", "z")
	writeFile(t, dir, "vendor/d.go", "ignored")

	all, err := ScanFiles(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("ScanFiles(0) = %d files, want 3 (vendor ignored)", len(all))
	}

	capped, err := ScanFiles(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 {
		t.Errorf("ScanFiles(2) = %d files, want 2", len(capped))
	}
}

// TestIndexContentExtractsDocuments proves the document-ingestion wiring: a
// supported document (HTML here) is converted to text before chunking, so the
// index holds the readable content — not the markup.
func TestIndexContentExtractsDocuments(t *testing.T) {
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{localAvailable: true}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	html := []byte("<html><body><h1>Payment retry</h1><p>exponential backoff</p></body></html>")
	created, err := idx.IndexContent(context.Background(), 1, "docs/guide.html", "m", html)
	if err != nil || created == 0 {
		t.Fatalf("IndexContent(html) = %d, err %v; want chunks", created, err)
	}
	got := strings.Join(fs.embedded, "\n")
	if strings.Contains(got, "<h1>") || strings.Contains(got, "<body>") {
		t.Errorf("HTML markup leaked into the index: %q", got)
	}
	if !strings.Contains(got, "Payment retry") || !strings.Contains(got, "exponential backoff") {
		t.Errorf("extracted text missing from index: %q", got)
	}
}

// TestIndexContentSkipsUnreadableDocument confirms a corrupt document never
// crashes the indexer and is skipped without a fatal error.
func TestIndexContentSkipsUnreadableDocument(t *testing.T) {
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{localAvailable: true}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	created, err := idx.IndexContent(context.Background(), 1, "broken.pdf", "m", []byte("this is not a real pdf"))
	if err != nil {
		t.Errorf("corrupt pdf should be skipped, not error: %v", err)
	}
	if created != 0 {
		t.Errorf("corrupt pdf produced %d chunks, want 0", created)
	}
}

// TestKeywordOnlyStoresTextWithoutEmbedding proves keyword-only mode never calls
// the embedder and stores chunks as text-only, so it works with no model at all.
func TestKeywordOnlyStoresTextWithoutEmbedding(t *testing.T) {
	fs := &fakeStore{}
	embedCalls := 0
	emb := &fakeEmbedder{localAvailable: true, onEmbed: func() { embedCalls++ }}
	idx := NewIndexer(fs, emb, 1, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32}).SetKeywordOnly(true)
	created, err := idx.IndexContent(context.Background(), 1, "notes.txt", "", []byte("exponential backoff and jitter"))
	if err != nil || created == 0 {
		t.Fatalf("IndexContent = %d, err %v; want chunks", created, err)
	}
	if embedCalls != 0 {
		t.Errorf("embedder called %d times in keyword-only mode; want 0", embedCalls)
	}
	if len(fs.embedded) != 0 {
		t.Errorf("keyword-only stored %d embedded chunks; want 0", len(fs.embedded))
	}
	if len(fs.textOnly) == 0 || !strings.Contains(strings.Join(fs.textOnly, " "), "exponential backoff") {
		t.Errorf("keyword-only did not store text-only chunks: %v", fs.textOnly)
	}
}
