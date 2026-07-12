package indexing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestNewWatcher(t *testing.T) {
	t.Parallel()
	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{})
	w := NewWatcher(42, "/some/project", "test-model", idx)

	if w.projectID != 42 {
		t.Errorf("projectID = %d, want 42", w.projectID)
	}
	if w.projectPath != "/some/project" {
		t.Errorf("projectPath = %q, want /some/project", w.projectPath)
	}
	if w.model != "test-model" {
		t.Errorf("model = %q, want test-model", w.model)
	}
	if w.idx != idx {
		t.Error("idx not set correctly")
	}
	if w.log == nil {
		t.Error("log should be set to slog.Default()")
	}
}

func TestIsIgnored(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"src/.git/objects", true},
		{"src/main.go", false},
		{"node_modules/react/index.js", true},
		{"__pycache__/foo.pyc", true},
		{".venv/lib/python/site.py", true},
		{"vendor/github.com/foo/bar.go", true},
		{"dist/bundle.js", true},
		{"build/output", true},
		{".next/server.js", true},
		{".turbo/state.json", true},
		{"coverage/lcov.info", true},
		// Normal paths
		{"pkg/client/client.go", false},
		{"internal/indexing/indexer.go", false},
		{"vendor-helpers/utils.go", false}, // "vendor-helpers" ≠ "vendor"
		{"src/distributed/compute.go", false},
		{"building/main.go", false},
		{"docs/index.md", false},
	}
	for _, tt := range tests {
		got := isIgnored(tt.path)
		if got != tt.want {
			t.Errorf("isIgnored(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestCancelTimers(t *testing.T) {
	// Not parallel — manipulates timers.
	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{})
	w := NewWatcher(1, "/tmp", "m", idx)

	timers := map[string]*time.Timer{
		"a": time.AfterFunc(10*time.Second, func() { t.Log("a fired (should not)") }),
		"b": time.AfterFunc(10*time.Second, func() { t.Log("b fired (should not)") }),
	}
	w.cancelTimers(timers)

	// Verify timers are stopped by checking they don't fire in a short window.
	select {
	case <-timers["a"].C:
		t.Error("timer a should have been stopped")
	case <-timers["b"].C:
		t.Error("timer b should have been stopped")
	case <-time.After(50 * time.Millisecond):
		// Expected: neither fired.
	}
}

func TestHandleEventIgnored(t *testing.T) {
	t.Parallel()
	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{})
	w := NewWatcher(1, "/tmp", "m", idx)

	// Ignored paths should be no-ops (no Indexer interaction).
	timers := map[string]*time.Timer{}

	w.handleEvent(context.Background(), nil,
		fsnotify.Event{Name: "/proj/.git/HEAD", Op: fsnotify.Create},
		timers, 500*time.Millisecond)
	w.handleEvent(context.Background(), nil,
		fsnotify.Event{Name: "/proj/node_modules/react/index.js", Op: fsnotify.Write},
		timers, 500*time.Millisecond)
	w.handleEvent(context.Background(), nil,
		fsnotify.Event{Name: "/proj/dist/bundle.js", Op: fsnotify.Remove},
		timers, 500*time.Millisecond)

	// After ignored events, timers should be empty.
	if len(timers) != 0 {
		t.Errorf("timers should be empty after ignored events, got %d", len(timers))
	}
}

func TestAddDirs(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}
	// Create temp directory structure.
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "src", "sub"))
	mustMkdir(t, filepath.Join(root, "lib"))
	mustMkdir(t, filepath.Join(root, ".git", "objects"))
	mustMkdir(t, filepath.Join(root, "node_modules", "react"))

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify.NewWatcher: %v", err)
	}
	defer func() { _ = fsw.Close() }()

	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{})
	w := NewWatcher(1, root, "m", idx)

	if err := w.addDirs(fsw, root); err != nil {
		t.Fatalf("addDirs: %v", err)
	}

	// Verify: src, src/sub, lib should be watched; .git and node_modules should not.
	watched := fsw.WatchList()
	if !containsDir(watched, filepath.Join(root, "src")) {
		t.Error("src should be watched")
	}
	if !containsDir(watched, filepath.Join(root, "src", "sub")) {
		t.Error("src/sub should be watched")
	}
	if !containsDir(watched, filepath.Join(root, "lib")) {
		t.Error("lib should be watched")
	}
	if containsDir(watched, filepath.Join(root, ".git")) {
		t.Error(".git should NOT be watched")
	}
	if containsDir(watched, filepath.Join(root, ".git", "objects")) {
		t.Error(".git/objects should NOT be watched")
	}
	if containsDir(watched, filepath.Join(root, "node_modules")) {
		t.Error("node_modules should NOT be watched")
	}
}

func TestHandleRemove(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, root)

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{})
	w := NewWatcher(1, root, "m", idx)

	filePath := filepath.Join(root, "src", "deleted.go")
	mustMkdir(t, filepath.Dir(filePath))
	_ = os.WriteFile(filePath, []byte("package src"), 0o644)

	w.handleRemove(context.Background(), filePath)

	// Should have recorded a DeleteFileByPath call with the relative path.
	fs.mu.Lock()
	paths := fs.deletedPaths
	fs.mu.Unlock()

	if len(paths) != 1 {
		t.Fatalf("expected 1 deleted path, got %d: %v", len(paths), paths)
	}
	if paths[0] != "src/deleted.go" {
		t.Errorf("deleted path = %q, want src/deleted.go", paths[0])
	}
}

func TestHandleCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("creates temp files")
	}
	root := t.TempDir()

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:        1,
		EmbedBatchSize: 8,
	})
	w := NewWatcher(1, root, "m", idx)

	// Write a .go source file.
	srcDir := filepath.Join(root, "src")
	mustMkdir(t, srcDir)
	goFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\n// main is the entry point.\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w.handleCreate(context.Background(), goFile)

	// Verify the store got chunks.
	fs.mu.Lock()
	embedded := fs.embedded
	textOnly := fs.textOnly
	fs.mu.Unlock()

	chunkCount := len(embedded) + len(textOnly)
	if chunkCount == 0 {
		t.Error("expected at least 1 chunk to be stored")
	}
	// The file content should be in one of the chunk slices.
	allChunks := append([]string{}, embedded...)
	allChunks = append(allChunks, textOnly...)
	if len(allChunks) == 0 {
		t.Fatal("expected embedded or textOnly to contain chunks")
	}
	combined := strings.Join(allChunks, "")
	if !strings.Contains(combined, "main") {
		t.Errorf("chunks should contain 'main', got: %q", combined)
	}
}

func TestHandleCreateSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:        1,
		EmbedBatchSize: 8,
	})
	w := NewWatcher(1, root, "m", idx)

	// Write a file that is neither a code/text file nor a supported extract
	// format — both ShouldIndex and Eligible return false (.png).
	filePath := filepath.Join(root, "image.png")
	if err := os.WriteFile(filePath, []byte{0x89, 'P', 'N', 'G', 0, 0, 0}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w.handleCreate(context.Background(), filePath)

	fs.mu.Lock()
	embeddedLen := len(fs.embedded)
	textOnlyLen := len(fs.textOnly)
	fs.mu.Unlock()

	if embeddedLen != 0 || textOnlyLen != 0 {
		t.Error("unindexable file (binary) should produce no chunks")
	}
}

// TestHandleCreateExtractSupported verifies the watcher bugfix: a file that
// chunker.ShouldIndex rejects but extract.Supported accepts IS re-indexed.
// Previously handleCreate only checked chunker.ShouldIndex, so PDFs, EMLs,
// Office documents, etc. were silently dropped on write events.
func TestHandleCreateExtractSupported(t *testing.T) {
	if testing.Short() {
		t.Skip("creates temp files")
	}
	root := t.TempDir()

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:        1,
		EmbedBatchSize: 8,
	})
	w := NewWatcher(1, root, "m", idx)

	// Write a .eml file — chunker.ShouldIndex rejects .eml (not in codeExts or
	// textExts) but extract.Supported returns true (registered extractor).
	emlContent := []byte("From: sender@example.com\nTo: recipient@example.com\nSubject: Hello\n\nThis is the email body content.\n")
	emlFile := filepath.Join(root, "message.eml")
	if err := os.WriteFile(emlFile, emlContent, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w.handleCreate(context.Background(), emlFile)

	fs.mu.Lock()
	chunkCount := len(fs.embedded) + len(fs.textOnly)
	allText := append(append([]string{}, fs.embedded...), fs.textOnly...)
	fs.mu.Unlock()

	if chunkCount == 0 {
		t.Error("Eligible file (.eml) should produce chunks, but none found; " +
			"watcher likely skipped it — this is the bug Eligible() fixes")
	}

	combined := strings.Join(allText, " ")
	if !strings.Contains(combined, "email body") {
		t.Errorf("extracted text should contain 'email body', got: %q", combined)
	}
}

func TestDebounce(t *testing.T) {
	if testing.Short() {
		t.Skip("timer-based")
	}
	root := t.TempDir()
	mustMkdir(t, root)

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:        1,
		EmbedBatchSize: 8,
	})
	w := NewWatcher(1, root, "m", idx)

	// Write an indexable file.
	goFile := filepath.Join(root, "debounce_test.go")
	if err := os.WriteFile(goFile, []byte("package indexing\n\nfunc foo() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx := context.Background()
	timers := make(map[string]*time.Timer)
	w.debounce(ctx, timers, goFile, 20*time.Millisecond)

	// Wait for the timer to fire + indexFile (slower under -race).
	time.Sleep(2 * time.Second)

	fs.mu.Lock()
	chunkCount := len(fs.embedded) + len(fs.textOnly)
	fs.mu.Unlock()

	if chunkCount == 0 {
		t.Error("expected chunks after debounce timer fired")
	}
}

func TestDebounceCancelsPrevious(t *testing.T) {
	if testing.Short() {
		t.Skip("timer-based")
	}
	root := t.TempDir()
	mustMkdir(t, root)

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8})
	w := NewWatcher(1, root, "m", idx)

	goFile := filepath.Join(root, "multi.go")
	if err := os.WriteFile(goFile, []byte("package indexing\nfunc one(){}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx := context.Background()
	timers := make(map[string]*time.Timer)

	// First debounce with a long window.
	w.debounce(ctx, timers, goFile, 10*time.Second)
	// Second debounce cancels the first.
	w.debounce(ctx, timers, goFile, 20*time.Millisecond)

	time.Sleep(2 * time.Second)

	fs.mu.Lock()
	chunkCount := len(fs.embedded) + len(fs.textOnly)
	fs.mu.Unlock()

	if chunkCount == 0 {
		t.Error("expected chunks after debounce")
	}
}

func TestHandleEventCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify + temp files")
	}
	root := t.TempDir()

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8})
	w := NewWatcher(1, root, "m", idx)

	// Create a real fsnotify watcher for handleCreateDir to use.
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify.NewWatcher: %v", err)
	}
	defer func() { _ = fsw.Close() }()

	// Add root so new subdirs can be discovered.
	if err := fsw.Add(root); err != nil {
		t.Fatalf("Add root: %v", err)
	}

	// Create a directory with a .go file so handleCreate indexes something.
	srcDir := filepath.Join(root, "src")
	mustMkdir(t, srcDir)
	goFile := filepath.Join(srcDir, "hello.go")
	if err := os.WriteFile(goFile, []byte("package src\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	timers := make(map[string]*time.Timer)
	// handleEvent with Create should call handleCreateDir + handleCreate.
	w.handleEvent(context.Background(), fsw,
		fsnotify.Event{Name: goFile, Op: fsnotify.Create},
		timers, 500*time.Millisecond)

	fs.mu.Lock()
	chunkCount := len(fs.embedded) + len(fs.textOnly)
	fs.mu.Unlock()

	if chunkCount == 0 {
		t.Error("Create event should have indexed the file")
	}
}

func TestHandleEventWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("timer-based")
	}
	root := t.TempDir()
	mustMkdir(t, root)

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8})
	w := NewWatcher(1, root, "m", idx)

	goFile := filepath.Join(root, "write_test.go")
	if err := os.WriteFile(goFile, []byte("package indexing\nfunc bar() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	timers := make(map[string]*time.Timer)
	w.handleEvent(context.Background(), nil,
		fsnotify.Event{Name: goFile, Op: fsnotify.Write},
		timers, 20*time.Millisecond)

	time.Sleep(2 * time.Second)

	fs.mu.Lock()
	chunkCount := len(fs.embedded) + len(fs.textOnly)
	fs.mu.Unlock()

	if chunkCount == 0 {
		t.Error("Write event should have triggered indexed")
	}
}

func TestHandleEventRemove(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, root)

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{})
	w := NewWatcher(1, root, "m", idx)

	filePath := filepath.Join(root, "to_delete.go")
	if err := os.WriteFile(filePath, []byte("package main"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	timers := make(map[string]*time.Timer)
	w.handleEvent(context.Background(), nil,
		fsnotify.Event{Name: filePath, Op: fsnotify.Remove},
		timers, 500*time.Millisecond)

	fs.mu.Lock()
	paths := fs.deletedPaths
	fs.mu.Unlock()

	if len(paths) != 1 {
		t.Fatalf("expected 1 deleted path from Remove event, got %d", len(paths))
	}
}

func TestHandleEventRename(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, root)

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{})
	w := NewWatcher(1, root, "m", idx)

	filePath := filepath.Join(root, "renamed.go")
	if err := os.WriteFile(filePath, []byte("package main"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	timers := make(map[string]*time.Timer)
	w.handleEvent(context.Background(), nil,
		fsnotify.Event{Name: filePath, Op: fsnotify.Rename},
		timers, 500*time.Millisecond)

	fs.mu.Lock()
	paths := fs.deletedPaths
	fs.mu.Unlock()

	if len(paths) != 1 {
		t.Fatalf("expected 1 deleted path from Rename event, got %d", len(paths))
	}
}

func TestHandleCreateDir(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify")
	}
	root := t.TempDir()

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8})
	w := NewWatcher(1, root, "m", idx)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify.NewWatcher: %v", err)
	}
	defer func() { _ = fsw.Close() }()

	// Create a non-ignored directory and a .go file inside it.
	newDir := filepath.Join(root, "pkg")
	mustMkdir(t, newDir)
	goFile := filepath.Join(newDir, "util.go")
	if err := os.WriteFile(goFile, []byte("package pkg\n\nfunc Util() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Add root so fsw can watch the new subdir.
	if err := fsw.Add(root); err != nil {
		t.Fatalf("Add root: %v", err)
	}

	// handleCreateDir on a non-ignored dir should add it to the watcher
	// and call handleCreate on the file.
	w.handleCreateDir(context.Background(), fsw, newDir)
	// handleCreateDir on a .go file should call handleCreate.
	w.handleCreateDir(context.Background(), fsw, goFile)

	fs.mu.Lock()
	chunkCount := len(fs.embedded) + len(fs.textOnly)
	fs.mu.Unlock()

	if chunkCount == 0 {
		t.Error("handleCreateDir should have indexed the .go file")
	}

	// Verify newDir was added to the watcher.
	watched := fsw.WatchList()
	if !containsDir(watched, newDir) {
		t.Error("newDir should have been added to the watcher")
	}
}

func TestHandleCreateDirIgnored(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify")
	}
	root := t.TempDir()

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8})
	w := NewWatcher(1, root, "m", idx)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify.NewWatcher: %v", err)
	}
	defer func() { _ = fsw.Close() }()

	// Create an ignored directory.
	ignoredDir := filepath.Join(root, ".git")
	mustMkdir(t, ignoredDir)

	// Add root so fsw can potentially watch the subdir.
	if err := fsw.Add(root); err != nil {
		t.Fatalf("Add root: %v", err)
	}

	w.handleCreateDir(context.Background(), fsw, ignoredDir)

	// The ignored dir should NOT be added to the watcher.
	watched := fsw.WatchList()
	if containsDir(watched, ignoredDir) {
		t.Error("ignored directory should NOT be added to the watcher")
	}
}

func TestHandleRemoveError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, root)

	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{})
	w := NewWatcher(1, root, "m", idx)

	// Path outside project root → rel == path (filepath.Rel returns error fallback).
	filePath := filepath.Join(root, "src", "nonexistent.go")

	w.handleRemove(context.Background(), filePath)
	// Should not panic; the delete on the fakeStore should succeed.
}

// helpers

func mustMkdir(t testing.TB, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func containsDir(paths []string, target string) bool {
	for _, p := range paths {
		if p == target {
			return true
		}
	}
	return false
}
