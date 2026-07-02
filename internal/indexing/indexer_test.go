package indexing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStore records how chunks were inserted (embedded vs text-only).
type fakeStore struct {
	store.Store
	nextID   int
	embedded []string
	textOnly []string
	status   string
}

func (f *fakeStore) UpsertProject(ctx context.Context, name, path, model string) (int, error) {
	return 1, nil
}
func (f *fakeStore) UpsertFile(ctx context.Context, projectID int, path, hash string, size int) (int, error) {
	f.nextID++
	return f.nextID, nil
}
func (f *fakeStore) DeleteChunksForFile(ctx context.Context, projectID, fileID, dims int) error {
	return nil
}
func (f *fakeStore) InsertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int) error {
	for _, c := range chunks {
		f.embedded = append(f.embedded, c.Content)
	}
	return nil
}
func (f *fakeStore) InsertChunksTextOnly(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, dims int) error {
	for _, c := range chunks {
		f.textOnly = append(f.textOnly, c.Content)
	}
	return nil
}
func (f *fakeStore) UpdateProjectStatus(ctx context.Context, id int, status string) error {
	f.status = status
	return nil
}

// fakeEmbedder returns fixed vectors; localAvailable controls whether a
// force-local ModelInfo succeeds (simulating a local provider being present).
type fakeEmbedder struct {
	embed.Embedder
	localAvailable bool
}

func (f *fakeEmbedder) ModelInfo(ctx context.Context, model string) (*embed.ModelInfo, error) {
	if !f.localAvailable {
		return nil, errors.New("no local provider")
	}
	return &embed.ModelInfo{Name: model, Dims: 3}, nil
}
func (f *fakeEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
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
	idx := NewIndexer(fs, &fakeEmbedder{localAvailable: false}, 3, false, false, "")

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
	idx := NewIndexer(fs, &fakeEmbedder{localAvailable: true}, 3, false, false, "")

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

func TestSkipsEmptyAndCountsChunks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.go", "   \n\t\n")
	writeFile(t, dir, "code.go", "package x\n\nfunc A() {}\n\nfunc B() {}\n")

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, false, false, "")

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

func TestScanFilesRespectsMaxAndIgnores(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "x")
	writeFile(t, dir, "b.go", "y")
	writeFile(t, dir, "c.go", "z")
	writeFile(t, dir, "vendor/d.go", "ignored")

	all, err := scanFiles(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("scanFiles(0) = %d files, want 3 (vendor ignored)", len(all))
	}

	capped, err := scanFiles(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 {
		t.Errorf("scanFiles(2) = %d files, want 2", len(capped))
	}
}
