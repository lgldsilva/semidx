package localstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// TestNewMkdirError covers the create-data-dir failure branch of New: the parent
// path is a regular file, so MkdirAll of a subdirectory under it fails.
func TestNewMkdirError(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(filepath.Join(f, "sub", "index.db")); err == nil {
		t.Error("New under a file path should fail to create the data dir")
	}
}

// TestNewNoParentDir covers the branch where filepath.Dir(path) == "." and the
// mkdir is skipped (a bare filename in the current directory).
func TestNewNoParentDir(t *testing.T) {
	t.Chdir(t.TempDir())
	s, err := New("index.db")
	if err != nil {
		t.Fatalf("New(bare filename): %v", err)
	}
	s.Close()
}

func TestGetProjectByIDNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetProjectByID(context.Background(), 99999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetProjectByID(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestGetProjectNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetProject(context.Background(), "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetProject(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestInsertChunksLengthMismatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, _ := s.UpsertProject(ctx, "p", "/p", "m", 0)
	fid, _ := s.UpsertFile(ctx, pid, "a.go", "h", 1)
	err := s.InsertChunks(ctx, pid, fid,
		[]chunker.Chunk{{Content: "a"}, {Content: "b"}},
		[][]float32{{1, 0}}, 2) // 2 chunks, 1 embedding
	if err == nil {
		t.Error("length mismatch between chunks and embeddings should error")
	}
}

func TestSearchEmptyResults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pid, _ := s.UpsertProject(ctx, "p", "/p", "m", 0)

	// No chunks at all → both searches return empty without error.
	if res, err := s.SearchSimilar(ctx, pid, []float32{1, 0, 0}, 3, 5); err != nil || len(res) != 0 {
		t.Errorf("SearchSimilar (empty) = %d results, err %v", len(res), err)
	}
	if res, err := s.SearchSimilarKeywords(ctx, pid, "anything", 3, 5); err != nil || len(res) != 0 {
		t.Errorf("SearchSimilarKeywords (empty) = %d results, err %v", len(res), err)
	}
	// topK <= 0 exercises the "no limit" (-1) branch.
	if res, err := s.SearchSimilarKeywords(ctx, pid, "anything", 3, 0); err != nil || len(res) != 0 {
		t.Errorf("SearchSimilarKeywords (topK=0) = %d results, err %v", len(res), err)
	}
}

func TestCosineSimilarityEdges(t *testing.T) {
	if got := cosineSimilarity([]float32{1, 2}, []float32{1, 2, 3}); got != 0 {
		t.Errorf("mismatched lengths = %v, want 0", got)
	}
	if got := cosineSimilarity(nil, nil); got != 0 {
		t.Errorf("empty vectors = %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{0, 0}, []float32{1, 1}); got != 0 {
		t.Errorf("zero vector = %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{1, 0}, []float32{1, 0}); got < 0.999 {
		t.Errorf("identical vectors = %v, want ~1", got)
	}
}

func TestIsUniqueViolationNonSQLiteError(t *testing.T) {
	if isUniqueViolation(errors.New("some other error")) {
		t.Error("a plain error must not be classified as a unique violation")
	}
	if isUniqueViolation(nil) {
		t.Error("nil error is not a unique violation")
	}
}

// TestClosedDBErrorPaths closes the DB then exercises the error-return branch of
// each method that talks to it, without asserting a specific driver message.
func TestClosedDBErrorPaths(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "closed.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Close()

	assertErr := func(name string, err error) {
		if err == nil {
			t.Errorf("%s on a closed DB = nil error, want error", name)
		}
	}

	if err := s.Ping(ctx); err == nil {
		t.Error("Ping on a closed DB should error")
	}
	_, err = s.UpsertProject(ctx, "p", "/p", "m", 0)
	assertErr("UpsertProject", err)
	_, err = s.CreateProject(ctx, "p", "m", "path", "", "", 0)
	assertErr("CreateProject", err)
	_, err = s.ListProjects(ctx, 0, 0)
	assertErr("ListProjects", err)
	assertErr("DeleteProject", s.DeleteProject(ctx, "p"))
	assertErr("UpdateProjectStatus", s.UpdateProjectStatus(ctx, 1, "ready"))
	_, err = s.UpsertFile(ctx, 1, "a.go", "h", 1)
	assertErr("UpsertFile", err)
	_, err = s.FileUpToDate(ctx, 1, "a.go", "h", 2)
	assertErr("FileUpToDate", err)
	_, err = s.ListFileHashes(ctx, 1)
	assertErr("ListFileHashes", err)
	assertErr("DeleteFileByPath", s.DeleteFileByPath(ctx, 1, "a.go"))
	assertErr("DeleteChunksForFile", s.DeleteChunksForFile(ctx, 1, 1, 2))
	assertErr("InsertChunks", s.InsertChunks(ctx, 1, 1, []chunker.Chunk{{Content: "x"}}, [][]float32{{1}}, 1))
	assertErr("InsertChunksTextOnly", s.InsertChunksTextOnly(ctx, 1, 1, []chunker.Chunk{{Content: "x"}}, 1))
	_, err = s.SearchSimilar(ctx, 1, []float32{1}, 1, 5)
	assertErr("SearchSimilar", err)
	_, err = s.SearchSimilarKeywords(ctx, 1, "word", 1, 5)
	assertErr("SearchSimilarKeywords", err)
	assertErr("DropAll", s.DropAll(ctx))
}

// TestConcurrentNew verifies that multiple goroutines (and, by extension,
// processes) can call New on the same database path without errors. The
// cross-process flock serialises schema initialisation so FTS5 virtual-table
// creation and trigger setup never race.
func TestConcurrentNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")

	var wg sync.WaitGroup
	errCh := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := New(path)
			if err != nil {
				errCh <- err
				return
			}
			s.Close()
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent New failed: %v", err)
		}
	}
}
