package indexing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/gitenv"
	"github.com/lgldsilva/semidx/internal/store"
)

// runGit runs a git command isolated from the host's global/system config so the
// test is hermetic (no core.hooksPath, no user identity leakage). This mirrors
// the established gotcha handling in internal/gitsync tests.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Also strip any inherited GIT_DIR/GIT_WORK_TREE so the command targets dir,
	// not an ambient repo leaked by a hook or bare-repo worktree.
	cmd.Env = append(gitenv.Clean(os.Environ()),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo creates a temp git repo with a couple of commits and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "tester")

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "initial commit")

	if err := os.WriteFile(filepath.Join(dir, "util.go"), []byte("package main\nfunc Util() int { return 42 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "add util")
	return dir
}

// TestNewIndexerDefaultsWorkers covers the workers<1, maxFileSize<1 and
// maxChunksPerFile<1 fallback branches.
func TestNewIndexerDefaultsWorkers(t *testing.T) {
	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{Workers: 0, EmbedBatchSize: 8, MaxFileSize: 0, MaxChunksPerFile: 0})
	if idx.workers != defaultIndexWorkers {
		t.Errorf("workers = %d, want default %d", idx.workers, defaultIndexWorkers)
	}
	if idx.maxFileSize != 1024*1024 {
		t.Errorf("maxFileSize = %d, want %d", idx.maxFileSize, 1024*1024)
	}
	if idx.maxChunksPerFile != 32 {
		t.Errorf("maxChunksPerFile = %d, want %d", idx.maxChunksPerFile, 32)
	}
	idx2 := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{Workers: 7, EmbedBatchSize: 8, MaxFileSize: 2 * 1024 * 1024, MaxChunksPerFile: 64})
	if idx2.workers != 7 {
		t.Errorf("workers = %d, want 7", idx2.workers)
	}
	if idx2.maxFileSize != 2*1024*1024 {
		t.Errorf("maxFileSize = %d, want %d", idx2.maxFileSize, 2*1024*1024)
	}
	if idx2.maxChunksPerFile != 64 {
		t.Errorf("maxChunksPerFile = %d, want %d", idx2.maxChunksPerFile, 64)
	}
}

// TestTruncateErr covers the error-string truncation helper.
func TestTruncateErr(t *testing.T) {
	if got := truncateErr(nil, 10); got != "" {
		t.Errorf("truncateErr(nil) = %q, want empty", got)
	}
	short := errors.New("short")
	if got := truncateErr(short, 10); got != "short" {
		t.Errorf("truncateErr(short) = %q, want short", got)
	}
	long := errors.New(strings.Repeat("x", 50))
	got := truncateErr(long, 10)
	if len(got) != 13 || !strings.HasSuffix(got, "...") { // 10 chars + "..."
		t.Errorf("truncateErr(long) = %q (len %d), want 10 chars + ...", got, len(got))
	}
}

// TestIndexGitHistorySuccess exercises the git-history indexing path end to end
// against a real temp repository.
func TestIndexGitHistorySuccess(t *testing.T) {
	dir := initRepo(t)
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, GitMode: true})

	stats, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if fs.status != "ready" {
		t.Errorf("status = %q, want ready", fs.status)
	}
	// Both the working-tree files and the git commits should have been embedded.
	joined := strings.Join(fs.embedded, "\n")
	if !strings.Contains(joined, "commit ") {
		t.Errorf("git commit content was not embedded; embedded=%q", joined)
	}
	if stats.FilesIndexed < 2 {
		t.Errorf("FilesIndexed = %d, want >=2 working-tree files", stats.FilesIndexed)
	}
}

// TestIndexGitHistoryVerbose runs the git path with verbose on to cover the
// verbose logging branches (logf/progress and the per-10-commits print).
func TestIndexGitHistoryVerbose(t *testing.T) {
	dir := initRepo(t)
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, Verbose: true, GitMode: true})

	if _, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
}

// TestIndexProjectGitHistoryErrorNonRepo covers the IndexProject branch where
// indexGitHistory fails (the path is not a git repository): it must count an
// error but still finish and mark the project ready.
func TestIndexProjectGitHistoryErrorNonRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir() // not a git repo
	writeFile(t, dir, "a.go", "package a\nfunc A() {}\n")

	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, GitMode: true})

	stats, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if stats.Errors == 0 {
		t.Error("expected a git-history error to be counted")
	}
	if fs.status != "ready" {
		t.Errorf("status = %q, want ready despite git error", fs.status)
	}
}

// TestIndexFileOpenError covers the os.Open failure branch of indexFile.
func TestIndexFileOpenError(t *testing.T) {
	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	_, _, outcome, _, err := idx.indexFile(context.Background(), 1, "/does/not/exist.go", "exist.go", "m")
	if err == nil {
		t.Error("indexFile on a missing path should return an error")
	}
	if outcome != outcomeSkippedEmpty {
		t.Errorf("outcome = %v, want outcomeSkippedEmpty", outcome)
	}
}

// --- error-injecting store for the hard-error paths -------------------------

type errStore struct {
	store.Store
	mu            sync.Mutex
	nextID        int
	upToDate      bool
	upsertFileErr error
	deleteErr     error
	insertErr     error
	insertTextErr error
}

func (e *errStore) FileUpToDate(ctx context.Context, projectID int, path, hash string, dims int) (bool, error) {
	return e.upToDate, nil
}
func (e *errStore) UpsertFile(ctx context.Context, projectID int, path, hash string, size int) (int, error) {
	if e.upsertFileErr != nil {
		return 0, e.upsertFileErr
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	return e.nextID, nil
}
func (e *errStore) DeleteChunksForFile(ctx context.Context, projectID, fileID, dims int) error {
	return e.deleteErr
}
func (e *errStore) InsertChunks(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, embeddings [][]float32, dims int) error {
	return e.insertErr
}
func (e *errStore) InsertChunksTextOnly(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, dims int) error {
	return e.insertTextErr
}
func (e *errStore) UpdateProjectStatus(ctx context.Context, id int, status string) error { return nil }

func TestIndexContentUpsertFileError(t *testing.T) {
	es := &errStore{upsertFileErr: errors.New("upsert boom")}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	if _, err := idx.IndexContent(context.Background(), 1, "a.go", "m", []byte("package a\nfunc A() {}\n")); err == nil {
		t.Error("UpsertFile error should surface as a hard error")
	}
}

func TestIndexContentDeleteChunksError(t *testing.T) {
	es := &errStore{deleteErr: errors.New("delete boom")}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	if _, err := idx.IndexContent(context.Background(), 1, "a.go", "m", []byte("package a\nfunc A() {}\n")); err == nil {
		t.Error("DeleteChunksForFile error should surface as a hard error")
	}
}

func TestIndexContentKeywordOnlyInsertError(t *testing.T) {
	es := &errStore{insertTextErr: errors.New("insert-text boom")}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32}).SetKeywordOnly(true)
	if _, err := idx.IndexContent(context.Background(), 1, "a.go", "m", []byte("package a\nfunc A() {}\n")); err == nil {
		t.Error("keyword-only InsertChunksTextOnly error should surface")
	}
}

// A sensitive file with no local provider stores text-only; if that insert
// fails, it must surface as a hard error.
func TestIndexContentSensitiveTextOnlyInsertError(t *testing.T) {
	es := &errStore{insertTextErr: errors.New("insert-text boom")}
	idx := NewIndexer(es, &fakeEmbedder{localAvailable: false}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	if _, err := idx.IndexContent(context.Background(), 1, "config/secret.txt", "gemini-embedding-2", []byte("API_KEY=xyz\n")); err == nil {
		t.Error("sensitive text-only InsertChunksTextOnly error should surface")
	}
}

// --- embed retry / soft-error paths ----------------------------------------

// flakyEmbedder fails its first (failCount) Embed calls, then succeeds.
type flakyEmbedder struct {
	fakeEmbedder
	mu        sync.Mutex
	failCount int
}

func (f *flakyEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCount > 0 {
		f.failCount--
		return nil, errors.New("transient")
	}
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

func TestEmbedWithRetrySucceedsAfterFailure(t *testing.T) {
	idx := NewIndexer(&fakeStore{}, &flakyEmbedder{failCount: 1}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	got, err := idx.embedWithRetry(context.Background(), "m", []string{"x"})
	if err != nil {
		t.Fatalf("embedWithRetry: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d embeddings, want 1", len(got))
	}
}

// alwaysFailEmbedder always errors on Embed.
type alwaysFailEmbedder struct{ fakeEmbedder }

func (f *alwaysFailEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	return nil, errors.New("permanent")
}

func TestEmbedWithRetryExhausts(t *testing.T) {
	idx := NewIndexer(&fakeStore{}, &alwaysFailEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	if _, err := idx.embedWithRetry(context.Background(), "m", []string{"x"}); err == nil {
		t.Error("embedWithRetry should return the last error after exhausting attempts")
	}
}

func TestEmbedWithRetryCancelDuringLoop(t *testing.T) {
	// An embedder that cancels the context and returns an error hits the
	// in-loop ctx.Err() early-return branch.
	ctx, cancel := context.WithCancel(context.Background())
	fe := &cancelEmbedder{cancel: cancel}
	idx := NewIndexer(&fakeStore{}, fe, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	if _, err := idx.embedWithRetry(ctx, "m", []string{"x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("embedWithRetry = %v, want context.Canceled", err)
	}
}

type cancelEmbedder struct {
	fakeEmbedder
	cancel context.CancelFunc
}

func (f *cancelEmbedder) Embed(ctx context.Context, model string, inputs ...string) ([][]float32, error) {
	f.cancel()
	return nil, errors.New("boom")
}

// TestEmbedAndInsertSoftErrorOnEmbedFailure: an embed batch that permanently
// fails is counted as a soft error, not a fatal one, and verbose logging is
// exercised.
func TestEmbedAndInsertSoftErrorOnEmbedFailure(t *testing.T) {
	fs := &fakeStore{}
	idx := NewIndexer(fs, &alwaysFailEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, Verbose: true})
	created, softErrs := idx.embedAndInsert(context.Background(), 1, 1,
		[]chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, "m", "a.go")
	if created != 0 {
		t.Errorf("created = %d, want 0 on embed failure", created)
	}
	if softErrs == 0 {
		t.Error("embed failure should count a soft error")
	}
}

// TestEmbedAndInsertSoftErrorOnInsertFailure: a failing InsertChunks is a soft
// error too (batch skipped, run continues).
func TestEmbedAndInsertSoftErrorOnInsertFailure(t *testing.T) {
	es := &errStore{insertErr: errors.New("insert boom")}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, Verbose: true})
	created, softErrs := idx.embedAndInsert(context.Background(), 1, 1,
		[]chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, "m", "a.go")
	if created != 0 {
		t.Errorf("created = %d, want 0 on insert failure", created)
	}
	if softErrs == 0 {
		t.Error("insert failure should count a soft error")
	}
}

// TestIndexGitHistoryNoMatchingCommits: a valid future --since matches no
// commits, so indexGitHistory produces no real commit content and never errors.
func TestIndexGitHistoryNoMatchingCommits(t *testing.T) {
	dir := initRepo(t)
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, GitMode: true, GitSince: "2099-01-01"})

	if _, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	// No actual commit was in range, so none of the "commit <hash>" content shows.
	if strings.Contains(strings.Join(fs.embedded, "\n"), "commit ") {
		t.Error("no git commits should be indexed for a future --since")
	}
}

// TestIndexGitHistoryContinuesOnInsertError: with a large commit and a store
// that fails InsertChunks, the git loop skips each commit (continue) — covering
// the commit-truncation and insert-error branches plus the verbose git print.
func TestIndexGitHistoryContinuesOnInsertError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "tester")
	// A file whose diff exceeds maxChunkChars, to exercise commit truncation.
	big := strings.Repeat("line of source code\n", 500)
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte("package main\n"+big), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "big commit")

	es := &errStore{insertErr: errors.New("insert boom")}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, Verbose: true, GitMode: true})

	if _, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
}

// gitEmbedFailer embeds batches fine (working tree) but fails EmbedSingle (git).
type gitEmbedFailer struct{ fakeEmbedder }

func (g *gitEmbedFailer) EmbedSingle(ctx context.Context, model, text string) ([]float32, error) {
	return nil, errors.New("single boom")
}

// TestIndexGitHistoryContinuesOnEmbedSingleError covers the git loop's
// continue-on-embed-error branch.
func TestIndexGitHistoryContinuesOnEmbedSingleError(t *testing.T) {
	dir := initRepo(t)
	fs := &fakeStore{}
	idx := NewIndexer(fs, &gitEmbedFailer{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, GitMode: true})

	if _, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	// EmbedSingle failed for every commit, so no commit content is stored.
	if strings.Contains(strings.Join(fs.embedded, "\n"), "commit ") {
		t.Error("commits should be skipped when EmbedSingle fails")
	}
}

// TestIndexContentTruncatesOversizedContent covers the maxFileSize truncation
// branch in indexContent.
func TestIndexContentTruncatesOversizedContent(t *testing.T) {
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	oversized := []byte("package x\n" + strings.Repeat("var _ = 1\n", 200000)) // >1MB
	if len(oversized) <= 1024*1024 {
		t.Fatalf("test content is only %d bytes, want > 1MB", len(oversized))
	}
	if _, err := idx.IndexContent(context.Background(), 1, "big.go", "bge-m3", oversized); err != nil {
		t.Fatalf("IndexContent: %v", err)
	}
}

// TestIndexContentCapsChunksPerFile covers the maxChunksPerFile truncation:
// a file with more blank-line-separated blocks than the cap.
func TestIndexContentCapsChunksPerFile(t *testing.T) {
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	var b strings.Builder
	for i := 0; i < 32+10; i++ {
		b.WriteString("func F() { return }\n\n") // each block → its own chunk
	}
	created, err := idx.IndexContent(context.Background(), 1, "many.go", "bge-m3", []byte(b.String()))
	if err != nil {
		t.Fatalf("IndexContent: %v", err)
	}
	if created != 32 {
		t.Errorf("created = %d, want capped at %d", created, 32)
	}
}

// TestVerboseProgress runs a small verbose index to cover the verbose branches
// of logf and progress (ReadMemStats path).
func TestVerboseProgress(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package a\nfunc A() {}\n")
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, Verbose: true})
	if _, err := idx.IndexProject(context.Background(), 1, dir, "bge-m3", 0); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
}
