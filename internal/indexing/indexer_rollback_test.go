package indexing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/store"
)

// versionedStore is a content-addressed in-memory IndexStore fake: file rows
// keyed by (path, hash) with per-row chunk counts, so FileUpToDate behaves
// like the real stores (row present AND ≥1 chunk). It records DeleteFileByID
// calls to prove rollback removes only the incompletely-indexed version.
type versionedStore struct {
	store.Store
	mu          sync.Mutex
	nextID      int
	rowID       map[string]int // path+"\x00"+hash → file ID
	chunks      map[int]int    // file ID → stored chunk count
	inserted    map[int]int    // file ID → chunks passed to InsertChunks (audit)
	deletedIDs  []int
	delPathCall int // DeleteFileByPath must NOT be used by the rollback
}

func newVersionedStore() *versionedStore {
	return &versionedStore{
		rowID:    make(map[string]int),
		chunks:   make(map[int]int),
		inserted: make(map[int]int),
	}
}

func rowKey(path, hash string) string { return path + "\x00" + hash }

func (v *versionedStore) FileUpToDate(_ context.Context, _ int, path, hash string, _ int) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	id, ok := v.rowID[rowKey(path, hash)]
	return ok && v.chunks[id] > 0, nil
}

func (v *versionedStore) UpsertFile(_ context.Context, _ int, path, hash string, _ int) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	key := rowKey(path, hash)
	if id, ok := v.rowID[key]; ok {
		return id, nil
	}
	v.nextID++
	v.rowID[key] = v.nextID
	return v.nextID, nil
}

func (v *versionedStore) DeleteChunksForFile(_ context.Context, _, fileID, _ int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.chunks[fileID] = 0
	return nil
}

func (v *versionedStore) DeleteFileByID(_ context.Context, _, fileID int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.deletedIDs = append(v.deletedIDs, fileID)
	for key, id := range v.rowID {
		if id == fileID {
			delete(v.rowID, key)
		}
	}
	delete(v.chunks, fileID)
	delete(v.inserted, fileID)
	return nil
}

func (v *versionedStore) DeleteFileByPath(context.Context, int, string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.delPathCall++
	return nil
}

func (v *versionedStore) InsertChunks(_ context.Context, _, fileID int, chunks []chunker.Chunk, _ [][]float32, _ int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.chunks[fileID] += len(chunks)
	v.inserted[fileID] += len(chunks)
	return nil
}

func (v *versionedStore) InsertChunksTextOnly(_ context.Context, _, fileID int, chunks []chunker.Chunk, _ int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.chunks[fileID] += len(chunks)
	return nil
}

func (v *versionedStore) InsertFileDependencies(context.Context, int, string, []string) error {
	return nil
}
func (v *versionedStore) EnsureEmbeddingCacheTable(context.Context, int) error { return nil }
func (v *versionedStore) LookupEmbeddingCache(context.Context, []string, string, int) (map[string][]float32, error) {
	return map[string][]float32{}, nil
}
func (v *versionedStore) InsertEmbeddingCache(context.Context, []string, string, [][]float32, int) error {
	return nil
}
func (v *versionedStore) PruneEmbeddingCache(context.Context, int) (int64, error) { return 0, nil }

// TestIndexContentPartialEmbedFailureRollsBack: when some embedding batches
// fail, the new file row must be rolled back — the file is NOT up to date
// afterwards, the previous hash version of the same path keeps its chunks, and
// a later pass with a healthy embedder re-indexes the file.
func TestIndexContentPartialEmbedFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore()

	// Seed a fully-indexed OLD version of the same path (old content, old hash).
	oldContent := []byte("package a\n\nfunc Old() {}\n")
	oldHash := ContentHash(oldContent)
	oldID, err := vs.UpsertFile(ctx, 1, "a.go", oldHash, len(oldContent))
	if err != nil {
		t.Fatalf("seed UpsertFile: %v", err)
	}
	if err := vs.InsertChunks(ctx, 1, oldID, []chunker.Chunk{{Content: string(oldContent), StartLine: 1, EndLine: 3}}, [][]float32{{1, 0, 0}}, 3); err != nil {
		t.Fatalf("seed InsertChunks: %v", err)
	}

	// New content producing several chunks (more than one embed batch).
	var sb strings.Builder
	sb.WriteString("package a\n\n")
	for i := 0; i < 12; i++ {
		sb.WriteString("func F" + strings.Repeat("x", i+1) + "() int { return 1 }\n")
	}
	content := []byte(sb.String())
	newHash := ContentHash(content)

	chunks := chunker.ChunkFile("a.go", content, 4000)
	if len(chunks) < 3 {
		t.Fatalf("need ≥3 chunks for a multi-batch file, got %d", len(chunks))
	}

	// The first embed batch fails permanently (3 retry attempts), the rest succeed.
	flaky := &flakyEmbedder{failCount: maxEmbedAttempts}
	idx := NewIndexer(vs, flaky, 3, IndexerOpts{
		Workers:          1,
		EmbedBatchSize:   2, // >1 batch for the chunk count above
		MaxFileSize:      1024 * 1024,
		MaxChunksPerFile: 64,
		Verbose:          true,
	})

	created, err := idx.IndexContent(ctx, 1, "a.go", "m", content)
	if err != nil {
		t.Fatalf("IndexContent returned hard error for soft batch failure: %v", err)
	}
	if created == 0 {
		t.Error("created = 0, want > 0 (the healthy batches are still stored)")
	}

	// The new (incomplete) version must be rolled back…
	upToDate, err := vs.FileUpToDate(ctx, 1, "a.go", newHash, 3)
	if err != nil || upToDate {
		t.Errorf("FileUpToDate(newHash) = %v, %v — want false, nil (partial indexing must not stick)", upToDate, err)
	}
	// …and ONLY that version: the old hash version survives with its chunks.
	upToDate, err = vs.FileUpToDate(ctx, 1, "a.go", oldHash, 3)
	if err != nil || !upToDate {
		t.Errorf("FileUpToDate(oldHash) = %v, %v — want true, nil (old version preserved)", upToDate, err)
	}
	if vs.delPathCall != 0 {
		t.Errorf("DeleteFileByPath called %d times — rollback must not delete all versions of the path", vs.delPathCall)
	}
	if len(vs.deletedIDs) != 1 || vs.deletedIDs[0] == oldID {
		t.Errorf("deletedIDs = %v, want exactly the new file's ID (old ID %d preserved)", vs.deletedIDs, oldID)
	}

	// A subsequent pass with a healthy embedder re-indexes the file completely.
	idx2 := NewIndexer(vs, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:          1,
		EmbedBatchSize:   2,
		MaxFileSize:      1024 * 1024,
		MaxChunksPerFile: 64,
	})
	created2, err := idx2.IndexContent(ctx, 1, "a.go", "m", content)
	if err != nil {
		t.Fatalf("re-index IndexContent: %v", err)
	}
	if created2 != len(chunks) {
		t.Errorf("re-index created = %d, want %d (all chunks)", created2, len(chunks))
	}
	upToDate, err = vs.FileUpToDate(ctx, 1, "a.go", newHash, 3)
	if err != nil || !upToDate {
		t.Errorf("after re-index FileUpToDate(newHash) = %v, %v — want true, nil", upToDate, err)
	}
}

// TestIndexContentInsertFailureRollsBack covers the same rollback when
// InsertChunks (not the embedder) is the failing step: every batch fails, so
// nothing is created and the file row must be removed.
func TestIndexContentInsertFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	es := &errStore{insertErr: errors.New("insert boom")}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:          1,
		EmbedBatchSize:   8,
		MaxFileSize:      1024 * 1024,
		MaxChunksPerFile: 32,
	})
	created, err := idx.IndexContent(ctx, 1, "a.go", "m", []byte("package a\nfunc A() {}\n"))
	if err != nil {
		t.Fatalf("IndexContent returned hard error for soft insert failure: %v", err)
	}
	if created != 0 {
		t.Errorf("created = %d, want 0", created)
	}
	if len(es.deletedIDs) != 1 {
		t.Errorf("deletedIDs = %v, want exactly 1 rollback of the new file row", es.deletedIDs)
	}
}

// TestIndexCommitRollsBackOnEmbedFailure: a commit whose embedding fails must
// not leave a chunkless file row behind.
func TestIndexCommitRollsBackOnEmbedFailure(t *testing.T) {
	es := &errStore{}
	idx := NewIndexer(es, &failSingleEmbedder{}, 3, IndexerOpts{
		Workers:          1,
		EmbedBatchSize:   8,
		MaxFileSize:      1024 * 1024,
		MaxChunksPerFile: 32,
	})
	if idx.indexCommit(context.Background(), 1, "m", []byte("commit abc123\n\nmessage")) {
		t.Error("indexCommit should return false when embedding fails")
	}
	if len(es.deletedIDs) != 1 {
		t.Errorf("deletedIDs = %v, want exactly 1 rollback of the commit file row", es.deletedIDs)
	}
}

// TestIndexCommitRollsBackOnInsertFailure: same rollback when InsertChunks fails.
func TestIndexCommitRollsBackOnInsertFailure(t *testing.T) {
	es := &errStore{insertErr: errors.New("insert boom")}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:          1,
		EmbedBatchSize:   8,
		MaxFileSize:      1024 * 1024,
		MaxChunksPerFile: 32,
	})
	if idx.indexCommit(context.Background(), 1, "m", []byte("commit def456\n\nmessage")) {
		t.Error("indexCommit should return false when InsertChunks fails")
	}
	if len(es.deletedIDs) != 1 {
		t.Errorf("deletedIDs = %v, want exactly 1 rollback of the commit file row", es.deletedIDs)
	}
}

// failSingleEmbedder fails EmbedSingle (git-commit path) while Embed works.
type failSingleEmbedder struct{ fakeEmbedder }

func (f *failSingleEmbedder) EmbedSingle(context.Context, string, string) ([]float32, error) {
	return nil, errors.New("single embed boom")
}

// TestIndexContentChunkBudgetTrimIsNotRollback: an intentional project
// chunk-budget trim is NOT an incomplete indexing — the file row must survive
// and no rollback may happen.
func TestIndexContentChunkBudgetTrimIsNotRollback(t *testing.T) {
	ctx := context.Background()
	vs := newVersionedStore()
	content := []byte("package a\n\nfunc A() {}\nfunc B() {}\nfunc C() {}\nfunc D() {}\n")
	chunks := chunker.ChunkFile("a.go", content, 4000)
	if len(chunks) < 2 {
		t.Fatalf("need ≥2 chunks, got %d", len(chunks))
	}

	idx := NewIndexer(vs, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:             1,
		EmbedBatchSize:      8,
		MaxFileSize:         1024 * 1024,
		MaxChunksPerFile:    32,
		MaxChunksPerProject: 1, // budget trims to a single chunk
		Verbose:             true,
	})
	created, err := idx.IndexContent(ctx, 1, "a.go", "m", content)
	if err != nil {
		t.Fatalf("IndexContent: %v", err)
	}
	if created != 1 {
		t.Errorf("created = %d, want 1 (budget-trimmed)", created)
	}
	if len(vs.deletedIDs) != 0 {
		t.Errorf("deletedIDs = %v, want none — a budget trim is not a failure", vs.deletedIDs)
	}
	upToDate, err := vs.FileUpToDate(ctx, 1, "a.go", ContentHash(content), 3)
	if err != nil || !upToDate {
		t.Errorf("FileUpToDate = %v, %v — budget-trimmed file must stay indexed", upToDate, err)
	}
}
