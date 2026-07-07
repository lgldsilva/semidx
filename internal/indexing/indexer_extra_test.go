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
	"time"

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
// maxChunksPerFile<1 fallback branches, plus the EmbedBatchSize default.
func TestNewIndexerDefaultsWorkers(t *testing.T) {
	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{Workers: 0, EmbedBatchSize: 0, MaxFileSize: 0, MaxChunksPerFile: 0})
	if idx.workers != defaultIndexWorkers {
		t.Errorf("workers = %d, want default %d", idx.workers, defaultIndexWorkers)
	}
	if idx.embedBatchSize != 8 {
		t.Errorf("embedBatchSize = %d, want 8", idx.embedBatchSize)
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
func (e *errStore) InsertFileDependencies(context.Context, int, string, []string) error  { return nil }
func (e *errStore) GetProjectCommit(ctx context.Context, projectID int) (string, error) {
	return "", nil
}
func (e *errStore) UpdateProjectCommit(ctx context.Context, projectID int, commitSHA string) error {
	return nil
}
func (e *errStore) FetchGraphPathsBFS(ctx context.Context, projectID int, seedPaths []string, maxDepth int) (map[string]int, error) {
	return nil, nil
}

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

// TestEmbedWithRetryCancelDuringBackoff: the first attempt fails, the context
// is cancelled during the second attempt's backoff (via AfterFunc), so
// embedWithRetry returns context.Canceled from the sleepBackoff path.
func TestEmbedWithRetryCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel well before the ~500ms backoff completes.
	time.AfterFunc(5*time.Millisecond, cancel)

	emb := &alwaysFailEmbedder{}
	idx := NewIndexer(&fakeStore{}, emb, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	if _, err := idx.embedWithRetry(ctx, "m", []string{"x"}); !errors.Is(err, context.Canceled) {
		t.Errorf("embedWithRetry = %v, want context.Canceled", err)
	}
}

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
		[]chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, "m", "a.go", nil)
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
		[]chunker.Chunk{{Content: "x", StartLine: 1, EndLine: 1}}, "m", "a.go", nil)
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
// branch in indexContent. Uses plain text (not Go) content to avoid tree-sitter
// parser issues with very large files (gotreesitter v0.21.0 known bug).
func TestIndexContentTruncatesOversizedContent(t *testing.T) {
	fs := &fakeStore{}
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{Workers: 4, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	oversized := []byte(strings.Repeat("plain text line not go code\n", 80000)) // >1MB, non-Go
	if len(oversized) <= 1024*1024 {
		t.Fatalf("test content is only %d bytes, want > 1MB", len(oversized))
	}
	if _, err := idx.IndexContent(context.Background(), 1, "big.txt", "bge-m3", oversized); err != nil {
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

// --- mergeOutcome unit tests -------------------------------------------------

func TestMergeOutcomeIndexedDominates(t *testing.T) {
	// If the unit is outcomeIndexed, the aggregate becomes outcomeIndexed regardless.
	if got := mergeOutcome(outcomeSkippedEmpty, outcomeIndexed); got != outcomeIndexed {
		t.Errorf("mergeOutcome(empty, indexed) = %v, want indexed", got)
	}
	if got := mergeOutcome(outcomeSkippedUnchanged, outcomeIndexed); got != outcomeIndexed {
		t.Errorf("mergeOutcome(unchanged, indexed) = %v, want indexed", got)
	}
	if got := mergeOutcome(outcomeIndexed, outcomeIndexed); got != outcomeIndexed {
		t.Errorf("mergeOutcome(indexed, indexed) = %v, want indexed", got)
	}
}

func TestMergeOutcomeUnchangedPromotedWhenNotIndexed(t *testing.T) {
	// From empty aggregate: unchanged unit promotes to unchanged.
	if got := mergeOutcome(outcomeSkippedEmpty, outcomeSkippedUnchanged); got != outcomeSkippedUnchanged {
		t.Errorf("mergeOutcome(empty, unchanged) = %v, want unchanged", got)
	}
	// From unchanged aggregate: empty unit stays unchanged.
	if got := mergeOutcome(outcomeSkippedUnchanged, outcomeSkippedEmpty); got != outcomeSkippedUnchanged {
		t.Errorf("mergeOutcome(unchanged, empty) = %v, want unchanged", got)
	}
}

func TestMergeOutcomeIndexedPreservesIndexed(t *testing.T) {
	// Once indexed, subsequent unchanged/empty units don't downgrade.
	if got := mergeOutcome(outcomeIndexed, outcomeSkippedUnchanged); got != outcomeIndexed {
		t.Errorf("mergeOutcome(indexed, unchanged) = %v, want indexed", got)
	}
	if got := mergeOutcome(outcomeIndexed, outcomeSkippedEmpty); got != outcomeIndexed {
		t.Errorf("mergeOutcome(indexed, empty) = %v, want indexed", got)
	}
}

func TestMergeOutcomeEncryptedPreservesCurrent(t *testing.T) {
	// Encrypted falls through the switch: current is returned unchanged.
	if got := mergeOutcome(outcomeSkippedEmpty, outcomeEncrypted); got != outcomeSkippedEmpty {
		t.Errorf("mergeOutcome(empty, encrypted) = %v, want empty", got)
	}
	if got := mergeOutcome(outcomeSkippedUnchanged, outcomeEncrypted); got != outcomeSkippedUnchanged {
		t.Errorf("mergeOutcome(unchanged, encrypted) = %v, want unchanged", got)
	}
}

// --- IndexProject -- maxChunksPerProject cap ----------------------------------

func TestIndexProjectMaxChunksPerProject(t *testing.T) {
	dir := t.TempDir()
	// Using .txt extension to avoid AST chunking (produces 1 chunk per file).
	writeFile(t, dir, "a.txt", "content a\n")
	writeFile(t, dir, "b.txt", "content b\n")

	fs := &fakeStore{}
	// Cap at 1 chunk per project: the first file's chunk is counted, the
	// second file's chunk triggers the cap warning in accumulate.
	idx := NewIndexer(fs, &fakeEmbedder{}, 3, IndexerOpts{
		Workers:             1,
		EmbedBatchSize:      8,
		MaxFileSize:         1024 * 1024,
		MaxChunksPerFile:    32,
		MaxChunksPerProject: 1, // only one chunk counted across all files
	})

	stats, err := idx.IndexProject(context.Background(), 1, dir, "m", 0)
	if err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	// Both files should be marked indexed.
	if stats.FilesIndexed != 2 {
		t.Errorf("FilesIndexed = %d, want 2", stats.FilesIndexed)
	}
	// But only 1 chunk should be counted (the cap).
	if stats.ChunksCreated != 1 {
		t.Errorf("ChunksCreated = %d, want 1 (capped)", stats.ChunksCreated)
	}
}

// --- InsertFileDependencies error path (best-effort, not fatal) ---------------

type depErrStore struct {
	*fakeStore
	depErr error
}

func (d *depErrStore) InsertFileDependencies(ctx context.Context, projectID int, sourceFile string, targets []string) error {
	return d.depErr
}

func TestIndexContentDependencyInsertErrorIsNonFatal(t *testing.T) {
	// When InsertFileDependencies fails, indexUnit logs a warning but the file
	// is still indexed. This test proves the non-fatal path.
	es := &depErrStore{
		fakeStore: &fakeStore{},
		depErr:    errors.New("dep table unavailable"),
	}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})

	// A Go file with a local (non-stdlib) import triggers InsertFileDependencies.
	// With empty modulePath, "mylib/util" is treated as a local import.
	created, err := idx.IndexContent(context.Background(), 1, "pkg/main.go", "m", []byte("package main\n\nimport \"mylib/util\"\n\nfunc main() { util.Run() }\n"))
	if err != nil {
		t.Fatalf("IndexContent should succeed despite dep insertion error: %v", err)
	}
	if created == 0 {
		t.Error("expected chunks to be created despite dep insertion error")
	}
}

// --- direct unit tests for accumulate branches -------------------------------

func TestAccumulateCountsEncryptedOutcome(t *testing.T) {
	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	stats := &IndexStats{}
	manifest := map[string]string{}

	idx.accumulate(stats, manifest, "secret.docx", fileResult{
		outcome: outcomeEncrypted,
		created: 0,
		units:   []indexedUnit{{path: "secret.docx", hash: "h1"}},
	})

	if stats.FilesEncrypted != 1 {
		t.Errorf("FilesEncrypted = %d, want 1", stats.FilesEncrypted)
	}
	if len(stats.EncryptedPaths) != 1 || stats.EncryptedPaths[0] != "secret.docx" {
		t.Errorf("EncryptedPaths = %v, want [secret.docx]", stats.EncryptedPaths)
	}
	if _, ok := manifest["secret.docx"]; !ok {
		t.Error("encrypted file should be recorded in the manifest")
	}
}

func TestAccumulateCountsFileError(t *testing.T) {
	idx := NewIndexer(&fakeStore{}, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32})
	stats := &IndexStats{}

	idx.accumulate(stats, map[string]string{}, "broken.go", fileResult{
		err:      errors.New("read error"),
		softErrs: 2, // combine with a hard error → both count
		outcome:  outcomeSkippedEmpty,
	})

	if stats.Errors != 3 { // 2 soft + 1 hard
		t.Errorf("Errors = %d, want 3 (2 soft + 1 hard)", stats.Errors)
	}
}

// --- direct unit test for recordWorktree error paths -------------------------

// worktreeErrStore extends fakeStore to inject errors in the worktree methods.
type worktreeErrStore struct {
	fakeStore
	setWorktreeErr       error
	pruneUnreferencedErr error
}

func (w *worktreeErrStore) SetWorktreeFiles(ctx context.Context, projectID int, worktree string, files map[string]string) error {
	return w.setWorktreeErr
}

func (w *worktreeErrStore) PruneUnreferencedFiles(ctx context.Context, projectID int) (int64, error) {
	return 0, w.pruneUnreferencedErr
}

func TestRecordWorktreeSetWorktreeFilesError(t *testing.T) {
	es := &worktreeErrStore{setWorktreeErr: errors.New("set-worktree boom")}
	// logf is exercised by setting Verbose.
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, Verbose: true})
	idx.worktree = "/wt"

	// Should not panic — the error is logged and the function returns early.
	idx.recordWorktree(context.Background(), 1, map[string]string{"a.go": "h1"})
}

func TestRecordWorktreePruneError(t *testing.T) {
	es := &worktreeErrStore{pruneUnreferencedErr: errors.New("prune boom")}
	idx := NewIndexer(es, &fakeEmbedder{}, 3, IndexerOpts{Workers: 1, EmbedBatchSize: 8, MaxFileSize: 1024 * 1024, MaxChunksPerFile: 32, Verbose: true})
	idx.worktree = "/wt"

	// SetWorktreeFiles succeeds, PruneUnreferencedFiles fails — the error is logged.
	idx.recordWorktree(context.Background(), 1, map[string]string{"a.go": "h1"})
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
