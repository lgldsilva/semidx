package indexing

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/localstore"
)

// TestGraphIntegrationDependenciesRecorded verifies that indexing a Go source
// file with local imports creates file_dependencies rows in the store.
func TestGraphIntegrationDependenciesRecorded(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()

	// Create a go.mod so the indexer can resolve the module path.
	writeFile(t, src, "go.mod", "module github.com/semidx/test\n\ngo 1.25\n")

	// A Go file that imports local packages.
	writeFile(t, src, "main.go", `package main

import (
	"github.com/semidx/test/internal/worker"
	"github.com/semidx/test/pkg/util"
)

func main() {
	worker.Run()
	util.Help()
}
`)

	// Another Go file with no imports.
	writeFile(t, src, "util.go", "package util\n\nfunc Help() string { return \"ok\" }\n")

	// A non-Go file that should be ignored by the import extractor.
	writeFile(t, src, "README.md", "# Test project\n")

	pid, err := st.UpsertProject(ctx, "proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	emb := &semanticEmbedder{}
	idx := NewIndexer(st, emb, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	stats, err := idx.IndexProject(ctx, pid, src, "m", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if stats.FilesScanned != 4 {
		t.Fatalf("FilesScanned = %d, want 4 (go.mod, main.go, util.go, README.md)", stats.FilesScanned)
	}

	// Fetch the dependency graph and verify main.go has two local imports.
	graph, err := st.FetchGraphNeighbors(ctx, pid)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors: %v", err)
	}

	deps, ok := graph["main.go"]
	if !ok {
		t.Fatal("main.go not found in dependency graph")
	}

	if len(deps) != 2 {
		t.Fatalf("main.go has %d dependencies, want 2: %v", len(deps), deps)
	}

	// Verify the target paths match what AnalyzeGoImports returns (directory
	// paths with trailing slash, module prefix stripped).
	wantDeps := map[string]bool{
		"internal/worker/": true,
		"pkg/util/":        true,
	}
	for _, d := range deps {
		if !wantDeps[d] {
			t.Errorf("unexpected dependency %q", d)
		}
		delete(wantDeps, d)
	}
	if len(wantDeps) > 0 {
		t.Errorf("missing dependencies: %v", wantDeps)
	}

	// Verify util.go has no dependencies (it has no imports).
	if deps, ok := graph["util.go"]; ok {
		t.Errorf("util.go unexpectedly has dependencies: %v", deps)
	}

	// Verify README.md is not in the graph at all.
	if deps, ok := graph["README.md"]; ok {
		t.Errorf("README.md unexpectedly has dependencies: %v", deps)
	}
}

// TestGraphIntegrationDependenciesReplaced verifies that re-indexing a file
// with changed imports replaces stale edges instead of accumulating them.
func TestGraphIntegrationDependenciesReplaced(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	writeFile(t, src, "go.mod", "module github.com/semidx/test\n\ngo 1.25\n")
	writeFile(t, src, "main.go", `package main

import "github.com/semidx/test/internal/worker"

func main() { worker.Run() }
`)

	pid, err := st.UpsertProject(ctx, "proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	emb := &semanticEmbedder{}
	idx := NewIndexer(st, emb, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	if _, err := idx.IndexProject(ctx, pid, src, "m", 0); err != nil {
		t.Fatalf("IndexProject (first): %v", err)
	}

	graph, err := st.FetchGraphNeighbors(ctx, pid)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors: %v", err)
	}
	if got := graph["main.go"]; len(got) != 1 || got[0] != "internal/worker/" {
		t.Fatalf("first index deps = %v, want [internal/worker/]", got)
	}

	// Change imports and re-index.
	writeFile(t, src, "main.go", `package main

import "github.com/semidx/test/pkg/util"

func main() { util.Help() }
`)
	if _, err := idx.IndexProject(ctx, pid, src, "m", 0); err != nil {
		t.Fatalf("IndexProject (second): %v", err)
	}

	graph, err = st.FetchGraphNeighbors(ctx, pid)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors: %v", err)
	}
	deps := graph["main.go"]
	if len(deps) != 1 {
		t.Fatalf("after re-index main.go has %d deps %v, want 1", len(deps), deps)
	}
	if deps[0] != "pkg/util/" {
		t.Fatalf("after re-index deps = %v, want [pkg/util/]", deps)
	}
}

// TestGraphIntegrationNoGoMod verifies that indexing a Go file without a go.mod
// still records dependencies (AnalyzeGoImports treats all non-stdlib imports as
// local when modulePath is empty).
func TestGraphIntegrationNoGoMod(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()

	// No go.mod — module path will be empty.
	writeFile(t, src, "handler.go", `package handler

import (
	"fmt"
	"context"
	"mylib/validate"
	"mylib/db"
)

func Handle(ctx context.Context) error {
	return validate.Check(ctx, db.Conn{})
}
`)

	pid, err := st.UpsertProject(ctx, "proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	emb := &semanticEmbedder{}
	idx := NewIndexer(st, emb, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	_, err = idx.IndexProject(ctx, pid, src, "m", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	graph, err := st.FetchGraphNeighbors(ctx, pid)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors: %v", err)
	}

	deps, ok := graph["handler.go"]
	if !ok {
		t.Fatal("handler.go not found in dependency graph")
	}

	// With empty modulePath, non-stdlib imports are kept as-is (with trailing slash).
	wantDeps := map[string]bool{
		"mylib/validate/": true,
		"mylib/db/":       true,
	}
	if len(deps) != 2 {
		t.Fatalf("handler.go has %d dependencies, want 2: %v", len(deps), deps)
	}
	for _, d := range deps {
		if !wantDeps[d] {
			t.Errorf("unexpected dependency %q", d)
		}
		delete(wantDeps, d)
	}
	if len(wantDeps) > 0 {
		t.Errorf("missing dependencies: %v", wantDeps)
	}
}

// TestGraphIntegrationNoGoRels verifies that non-Go files don't produce
// dependency records (and no crash occurs).
func TestGraphIntegrationNoGoRels(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()

	writeFile(t, src, "data.csv", "a,b,c\n1,2,3\n")
	writeFile(t, src, "notes.txt", "just some text\n")
	writeFile(t, src, "config.json", `{"key": "value"}`)

	pid, err := st.UpsertProject(ctx, "proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	emb := &semanticEmbedder{}
	idx := NewIndexer(st, emb, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	_, err = idx.IndexProject(ctx, pid, src, "m", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	graph, err := st.FetchGraphNeighbors(ctx, pid)
	if err != nil {
		t.Fatalf("FetchGraphNeighbors: %v", err)
	}

	if len(graph) != 0 {
		t.Errorf("expected empty dependency graph for non-Go files, got %v", graph)
	}
}

// TestGraphIntegrationWorktreeTracking verifies that SetWorktree and
// recordWorktree code paths are exercised — the worktree manifest is recorded
// and file versions are tracked. Uses a real SQLite store.
func TestGraphIntegrationWorktreeTracking(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	src := t.TempDir()
	writeFile(t, src, "go.mod", "module github.com/semidx/wt\n\ngo 1.25\n")
	writeFile(t, src, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println() }\n")

	pid, err := st.UpsertProject(ctx, "wt-proj", src, "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	emb := &semanticEmbedder{}
	idx := NewIndexer(st, emb, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	idx.SetWorktree("/tmp/my-worktree")

	stats, err := idx.IndexProject(ctx, pid, src, "m", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if stats.FilesScanned != 2 {
		t.Fatalf("FilesScanned = %d, want 2", stats.FilesScanned)
	}
	if stats.FilesIndexed == 0 {
		t.Fatal("expected at least one file indexed")
	}
	// The worktree manifest should have been recorded — re-indexing should
	// prune unreferenced files (best-effort). A second index proves idempotency.
	stats2, err := idx.IndexProject(ctx, pid, src, "m", 0)
	if err != nil {
		t.Fatalf("second IndexProject: %v", err)
	}
	if stats2.FilesSkipped != 2 {
		t.Errorf("second run FilesSkipped = %d, want 2 (unchanged files)", stats2.FilesSkipped)
	}
}

// TestGraphIntegrationEncryptedDocument verifies that indexExtracted's
// ErrEncrypted / CanBeEncrypted branch is exercised: a file with OLE magic
// bytes is flagged as outcomeEncrypted (no chunks created, no error).
func TestGraphIntegrationEncryptedDocument(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	st, err := localstore.New(dbPath)
	if err != nil {
		t.Fatalf("localstore.New: %v", err)
	}
	t.Cleanup(st.Close)

	pid, err := st.UpsertProject(ctx, "enc-proj", "/tmp/enc", "m", 0)
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	emb := &semanticEmbedder{}
	idx := NewIndexer(st, emb, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	// OLE magic bytes (the signature of an OLE2 Compound File) will be detected
	// as an encrypted Office document by the extract package.
	oleMagic := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

	// A .docx file with OLE magic should be handled by indexExtracted, which
	// returns outcomeEncrypted.
	created, err := idx.IndexContent(ctx, pid, "report.docx", "m", oleMagic)
	if err != nil {
		t.Fatalf("IndexContent(encrypted docx): %v", err)
	}
	if created != 0 {
		t.Errorf("encrypted document produced %d chunks, want 0", created)
	}
}
