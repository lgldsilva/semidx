// Package indexing walks a project, chunks each file and stores embeddings,
// routing sensitive files away from cloud providers. It depends only on the
// store.IndexStore and embed.Embedder interfaces, so the pipeline is
// unit-testable with fakes and can run against a standalone local store.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/extract"
	"github.com/lgldsilva/semidx/internal/gitenv"
	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/store"
)

const (
	maxFileSize         = 1024 * 1024 // 1MB
	maxChunkChars       = 4000        // ~1000 tokens for bge-m3
	maxChunksPerFile    = 32
	logEvery            = 10
	embedBatchSize      = 8
	defaultIndexWorkers = 4
)

// Indexer indexes a project into an IndexStore using an Embedder.
type Indexer struct {
	db          store.IndexStore
	embedder    embed.Embedder
	dims        int
	workers     int
	verbose     bool
	gitMode     bool
	gitSince    string
	keywordOnly bool   // when true, store text-only (no embeddings) for keyword search
	worktree    string // when set, record this worktree's manifest + prune after indexing
}

// IndexStats summarizes an indexing run.
type IndexStats struct {
	FilesScanned  int
	FilesIndexed  int
	FilesSkipped  int // unchanged since last index (incremental)
	ChunksCreated int
	Errors        int
	// FilesEncrypted counts password-protected files that were skipped but CAN be
	// unlocked with a password; EncryptedPaths lists their project-relative paths
	// so the caller can offer `semidx unlock`.
	FilesEncrypted int
	EncryptedPaths []string
}

// fileOutcome is how indexFile handled a single file.
type fileOutcome int

const (
	outcomeIndexed          fileOutcome = iota // (re)embedded and stored
	outcomeSkippedEmpty                        // empty or produced no chunks
	outcomeSkippedUnchanged                    // already indexed with the same hash
	outcomeEncrypted                           // password-protected; skipped, unlockable
)

// fileResult is what indexFile produced for one file: chunk/error counts, how it
// was handled, and the searchable units to record in the worktree manifest.
type fileResult struct {
	created  int
	softErrs int
	outcome  fileOutcome
	units    []indexedUnit
	err      error
}

// NewIndexer wires an Indexer. dims is the embedding dimension of model;
// workers is the file concurrency (<1 falls back to defaultIndexWorkers).
func NewIndexer(db store.IndexStore, emb embed.Embedder, dims, workers int, verbose, gitMode bool, gitSince string) *Indexer {
	if workers < 1 {
		workers = defaultIndexWorkers
	}
	return &Indexer{db: db, embedder: emb, dims: dims, workers: workers, verbose: verbose, gitMode: gitMode, gitSince: gitSince}
}

// SetKeywordOnly switches the indexer to keyword-only mode: chunks are stored as
// text (embedding NULL) and no embedding provider is called. Returns the indexer
// for chaining.
func (idx *Indexer) SetKeywordOnly(v bool) *Indexer {
	idx.keywordOnly = v
	return idx
}

// SetWorktree marks the indexed tree as a specific git worktree. After indexing,
// the indexer records that worktree's (path -> hash) manifest and prunes file
// versions no worktree references. Empty (the default) disables worktree tracking
// (document folders and push indexing).
func (idx *Indexer) SetWorktree(worktree string) *Indexer {
	idx.worktree = worktree
	return idx
}

// IndexProject scans projectPath, indexes each eligible file, optionally indexes
// git history, and marks the project ready.
func (idx *Indexer) IndexProject(ctx context.Context, projectID int, projectPath, model string, maxFiles int) (*IndexStats, error) {
	stats := &IndexStats{}

	files, err := ScanFiles(projectPath, maxFiles)
	if err != nil {
		return nil, err
	}
	stats.FilesScanned = len(files)

	// Index files concurrently, bounded to idx.workers. Files are independent
	// (distinct file_id rows, pool-safe DB access); a mutex guards the shared
	// stats. Per-file errors are counted, not fatal, so goroutines return nil
	// and errgroup is used purely for the bounded fan-out.
	var (
		mu        sync.Mutex
		processed int
		g         errgroup.Group
		manifest  = map[string]string{} // rel -> hash of files present in this worktree
	)
	g.SetLimit(idx.workers)

	for _, path := range files {
		if ctx.Err() != nil {
			break // stop scheduling on Ctrl-C / SIGTERM; in-flight files finish
		}
		path := path
		g.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			rel, _ := filepath.Rel(projectPath, path)
			created, softErrs, outcome, units, ferr := idx.indexFile(ctx, projectID, path, rel, model)

			mu.Lock()
			idx.accumulate(stats, manifest, rel, fileResult{
				created:  created,
				softErrs: softErrs,
				outcome:  outcome,
				units:    units,
				err:      ferr,
			})
			processed++
			done, chunks := processed, stats.ChunksCreated
			mu.Unlock()

			idx.progress(done, len(files), rel, chunks)
			return nil
		})
	}
	_ = g.Wait()

	if err := ctx.Err(); err != nil {
		return stats, err // cancelled: leave the project as 'indexing'
	}

	// Worktree tracking: record this worktree's manifest, then prune file versions
	// no worktree still references (bounding growth). Best-effort — a manifest
	// failure shouldn't fail an otherwise-successful index.
	if idx.worktree != "" {
		idx.recordWorktree(ctx, projectID, manifest)
	}

	if idx.gitMode {
		if err := idx.indexGitHistory(ctx, projectID, projectPath, model); err != nil {
			stats.Errors++
			idx.logf("[err] git history: %s", truncateErr(err, 200))
		}
	}

	_ = idx.db.UpdateProjectStatus(ctx, projectID, "ready")
	return stats, nil
}

// accumulate folds one file's indexing result into the run stats and the
// worktree manifest. Callers must hold the stats mutex.
func (idx *Indexer) accumulate(stats *IndexStats, manifest map[string]string, rel string, r fileResult) {
	stats.Errors += r.softErrs
	switch {
	case r.err != nil:
		stats.Errors++
		idx.logf("[err] %s: %s", rel, truncateErr(r.err, 200))
	case r.outcome == outcomeIndexed:
		stats.ChunksCreated += r.created
		stats.FilesIndexed++
	case r.outcome == outcomeSkippedUnchanged:
		stats.FilesSkipped++
	case r.outcome == outcomeEncrypted:
		stats.FilesEncrypted++
		stats.EncryptedPaths = append(stats.EncryptedPaths, rel)
	}
	// Record every searchable unit (a file, or an archive's entries) in the
	// worktree manifest so pruning matches the actual stored paths.
	for _, u := range r.units {
		manifest[u.path] = u.hash
	}
}

// recordWorktree stores this worktree's manifest and then prunes file versions
// no worktree still references. Best-effort: a manifest failure is logged and
// skips the prune, but never fails an otherwise-successful index.
func (idx *Indexer) recordWorktree(ctx context.Context, projectID int, manifest map[string]string) {
	if err := idx.db.SetWorktreeFiles(ctx, projectID, idx.worktree, manifest); err != nil {
		idx.logf("[warn] worktree manifest: %s", truncateErr(err, 200))
		return
	}
	if _, err := idx.db.PruneUnreferencedFiles(ctx, projectID); err != nil {
		idx.logf("[warn] prune: %s", truncateErr(err, 200))
	}
}

// ScanFiles walks projectPath, skipping ignored dirs and non-indexable files,
// and caps the result at maxFiles (0 = unlimited).
func ScanFiles(projectPath string, maxFiles int) ([]string, error) {
	var files []string
	err := filepath.WalkDir(projectPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if chunker.IsIgnoredDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(projectPath, path)
		if chunker.ShouldIndex(rel) || extract.Supported(rel) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if maxFiles > 0 && len(files) > maxFiles {
		files = files[:maxFiles]
	}
	return files, nil
}

// indexFile reads a file from disk and indexes its content. The outcome
// distinguishes an indexed file from one skipped because it's empty/chunk-less
// or unchanged; hardErr signals a read/upsert/delete failure; softErrs counts
// failed embed sub-batches.
func (idx *Indexer) indexFile(ctx context.Context, projectID int, path, rel, model string) (created, softErrs int, outcome fileOutcome, units []indexedUnit, hardErr error) {
	// #nosec G304 -- indexing a file the caller pointed us at is the whole job; path comes from the project walk.
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, nil, err
	}
	content, err := io.ReadAll(io.LimitReader(f, maxFileSize))
	_ = f.Close()
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, nil, err
	}
	return idx.indexContent(ctx, projectID, rel, model, content)
}

// IndexContent indexes one file's in-memory content (used by the push files API)
// and returns the number of chunks created.
func (idx *Indexer) IndexContent(ctx context.Context, projectID int, rel, model string, content []byte) (int, error) {
	created, _, _, _, err := idx.indexContent(ctx, projectID, rel, model, content)
	return created, err
}

// indexedUnit is one stored, searchable unit (a file, or an archive entry) and
// its content hash — collected into the worktree manifest.
type indexedUnit struct {
	path string
	hash string
}

// IndexEncryptedFile decrypts a password-protected file with the given password
// and indexes it. It returns unlocked=false with a nil error when the password
// is wrong (so the caller can try another candidate); a non-nil error is a real
// failure (read/parse/store). On success it returns the stored units so the
// caller can add them to the worktree manifest. Used by `semidx unlock`.
func (idx *Indexer) IndexEncryptedFile(ctx context.Context, projectID int, path, rel, model, password string) (unlocked bool, units []indexedUnit, err error) {
	// #nosec G304 -- path comes from the project walk / pending list, like indexFile.
	f, err := os.Open(path)
	if err != nil {
		return false, nil, err
	}
	content, err := io.ReadAll(io.LimitReader(f, maxFileSize))
	_ = f.Close()
	if err != nil {
		return false, nil, err
	}

	docs, err := extract.ExtractAllWithPassword(rel, content, password)
	switch {
	case errors.Is(err, extract.ErrWrongPassword), errors.Is(err, extract.ErrEncrypted):
		return false, nil, nil // wrong/again-needed password — let the caller try another
	case err != nil:
		return false, nil, err
	}
	for _, d := range docs {
		_, _, _, h, e := idx.indexUnit(ctx, projectID, d.Path, model, []byte(d.Text))
		if e != nil {
			return false, units, e
		}
		if h != "" {
			units = append(units, indexedUnit{d.Path, h})
		}
	}
	return true, units, nil
}

// indexContent expands one input into searchable units and indexes each. A code
// or plain-text file is a single unit; a document (PDF/Office/HTML) is its
// extracted text; an archive (.jar/.war) fans out to one unit per entry
// (class API surface + source/text resources). Shared by the disk and push paths.
func (idx *Indexer) indexContent(ctx context.Context, projectID int, rel, model string, content []byte) (created, softErrs int, outcome fileOutcome, units []indexedUnit, hardErr error) {
	if len(strings.TrimSpace(string(content))) == 0 {
		return 0, 0, outcomeSkippedEmpty, nil, nil
	}
	if len(content) > maxFileSize {
		content = content[:maxFileSize]
	}

	if extract.Supported(rel) {
		return idx.indexExtracted(ctx, projectID, rel, model, content)
	}

	c, s, o, h, e := idx.indexUnit(ctx, projectID, rel, model, content)
	if h != "" {
		units = []indexedUnit{{rel, h}}
	}
	return c, s, o, units, e
}

// indexExtracted extracts a document/archive into its searchable units and
// indexes each, aggregating their outcomes. A password-protected input is
// flagged (or silently skipped when we can't decrypt its type); a malformed one
// is a soft error, never fatal.
func (idx *Indexer) indexExtracted(ctx context.Context, projectID int, rel, model string, content []byte) (created, softErrs int, outcome fileOutcome, units []indexedUnit, hardErr error) {
	docs, err := extract.ExtractAll(rel, content)
	switch {
	case errors.Is(err, extract.ErrEncrypted):
		// Password-protected: skip now, but if the type is one we can decrypt,
		// flag it so the caller can prompt for a password (`semidx unlock`).
		if extract.CanBeEncrypted(rel) {
			return 0, 0, outcomeEncrypted, nil, nil
		}
		return 0, 0, outcomeSkippedEmpty, nil, nil
	case err != nil:
		return 0, 1, outcomeSkippedEmpty, nil, nil // malformed: soft error, never fatal
	}
	outcome = outcomeSkippedEmpty
	for _, d := range docs {
		c, s, o, h, e := idx.indexUnit(ctx, projectID, d.Path, model, []byte(d.Text))
		if e != nil {
			return created, softErrs, outcomeSkippedEmpty, units, e
		}
		created += c
		softErrs += s
		if h != "" {
			units = append(units, indexedUnit{d.Path, h})
		}
		outcome = mergeOutcome(outcome, o)
	}
	return created, softErrs, outcome, units, nil
}

// mergeOutcome folds one unit's outcome into the aggregate for a multi-unit
// input: any indexed unit makes the whole input indexed; otherwise an unchanged
// unit upgrades an empty aggregate to unchanged.
func mergeOutcome(current, o fileOutcome) fileOutcome {
	switch o {
	case outcomeIndexed:
		return outcomeIndexed
	case outcomeSkippedUnchanged:
		if current != outcomeIndexed {
			return outcomeSkippedUnchanged
		}
	}
	return current
}

// indexUnit chunks, embeds and stores one already-textual unit (a file's content
// or an archive entry's text), returning its content hash for the manifest.
func (idx *Indexer) indexUnit(ctx context.Context, projectID int, rel, model string, content []byte) (created, softErrs int, outcome fileOutcome, hash string, hardErr error) {
	if len(strings.TrimSpace(string(content))) == 0 {
		return 0, 0, outcomeSkippedEmpty, "", nil
	}
	hash = fmt.Sprintf("%x", sha256.Sum256(content))

	// Incremental: skip a unit already indexed with this exact content.
	if upToDate, err := idx.db.FileUpToDate(ctx, projectID, rel, hash, idx.dims); err == nil && upToDate {
		return 0, 0, outcomeSkippedUnchanged, hash, nil
	}

	fileID, err := idx.db.UpsertFile(ctx, projectID, rel, hash, len(content))
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, "", err
	}

	chunks := chunker.ChunkFile(rel, content, maxChunkChars)
	if len(chunks) == 0 {
		return 0, 0, outcomeSkippedEmpty, "", nil
	}
	if len(chunks) > maxChunksPerFile {
		chunks = chunks[:maxChunksPerFile]
	}

	if err := idx.db.DeleteChunksForFile(ctx, projectID, fileID, idx.dims); err != nil {
		return 0, 0, outcomeSkippedEmpty, "", err
	}

	created, softErrs, err = idx.storeChunks(ctx, projectID, fileID, rel, model, chunks)
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, "", err
	}
	return created, softErrs, outcomeIndexed, hash, nil
}

// storeChunks turns a unit's chunks into stored rows, honoring keyword-only mode
// and privacy routing. It stores the chunks text-only in keyword-only mode, or
// when a sensitive file cannot be embedded by a local provider; otherwise it
// embeds them (forcing a local provider for sensitive files).
func (idx *Indexer) storeChunks(ctx context.Context, projectID, fileID int, rel, model string, chunks []chunker.Chunk) (created, softErrs int, hardErr error) {
	// Keyword-only mode: no embedding provider at all — store the chunks as text
	// so they stay searchable by keyword (FTS/ILIKE) without any model.
	if idx.keywordOnly {
		if err := idx.db.InsertChunksTextOnly(ctx, projectID, fileID, chunks, idx.dims); err != nil {
			return 0, 0, err
		}
		return len(chunks), 0, nil
	}

	// Privacy routing: a sensitive file must never be embedded by a cloud
	// provider. If a local provider can serve the model, embed locally; if not,
	// store the content as text-only (embedding NULL) so it stays findable via
	// keyword search without ever leaving the machine.
	embedCtx := ctx
	if privacy.IsSensitive(rel) {
		localCtx := embed.WithForceLocal(ctx, true)
		if _, err := idx.embedder.ModelInfo(localCtx, model); err != nil {
			if err := idx.db.InsertChunksTextOnly(ctx, projectID, fileID, chunks, idx.dims); err != nil {
				return 0, 0, err
			}
			idx.logf("  [local-text] %s (sensitive; stored text-only, no cloud embedding)", rel)
			return len(chunks), 0, nil
		}
		embedCtx = localCtx
	}

	created, softErrs = idx.embedAndInsert(embedCtx, projectID, fileID, chunks, model, rel)
	return created, softErrs, nil
}

// embedAndInsert embeds chunks in sub-batches and inserts each successful batch.
func (idx *Indexer) embedAndInsert(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, model, rel string) (created, softErrs int) {
	for start := 0; start < len(chunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		sub := chunks[start:end]
		inputs := make([]string, len(sub))
		for j, c := range sub {
			inputs[j] = c.Content
		}

		embeddings, err := idx.embedWithRetry(ctx, model, inputs)
		if err != nil {
			softErrs++
			idx.logf("[err] embed %s batch %d-%d: %s", rel, start, end, truncateErr(err, 200))
			continue
		}
		if err := idx.db.InsertChunks(ctx, projectID, fileID, sub, embeddings, idx.dims); err != nil {
			softErrs++
			idx.logf("[err] insert %s batch %d-%d: %s", rel, start, end, truncateErr(err, 200))
			continue
		}
		created += len(sub)
	}
	return created, softErrs
}

const maxEmbedAttempts = 3

func (idx *Indexer) embedWithRetry(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	var lastErr error
	for attempt := 0; attempt < maxEmbedAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepBackoff(ctx, attempt); err != nil {
				return nil, err // context cancelled during backoff
			}
		}
		embeddings, err := idx.embedder.Embed(ctx, model, inputs...)
		if err == nil {
			return embeddings, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

// sleepBackoff waits an exponentially growing delay (500ms, 1s, 2s, …) plus up
// to 50% jitter, returning early with ctx.Err() if the context is cancelled.
func sleepBackoff(ctx context.Context, attempt int) error {
	backoff := (500 * time.Millisecond) << (attempt - 1)
	// #nosec G404 -- jitter for retry backoff, not security-sensitive; math/rand is fine.
	delay := backoff + time.Duration(rand.Int64N(int64(backoff/2)))
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (idx *Indexer) indexGitHistory(ctx context.Context, projectID int, projectPath, model string) error {
	// #nosec G204 -- fixed "git" executable; projectPath is a local dir and gitSince a bounded flag value.
	cmd := exec.Command("git", "-C", projectPath, "log", "-p", "--since="+idx.gitSince)
	// Strip any inherited GIT_DIR/GIT_WORK_TREE so `git -C projectPath` reads that
	// repo's history, not an ambient repo leaked by a hook or bare-repo worktree.
	cmd.Env = gitenv.Clean(cmd.Environ())
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git log -p: %w", err)
	}
	if len(out) == 0 {
		return nil
	}

	commits := bytes.Split(out, []byte("\ncommit "))
	for i, commit := range commits {
		if len(commit) == 0 {
			continue
		}
		if i > 0 {
			commit = append([]byte("commit "), commit...)
		}
		if idx.indexCommit(ctx, projectID, model, commit) && idx.verbose && i%10 == 0 {
			fmt.Printf("  [git] processed %d commits\n", i+1)
		}
	}
	return nil
}

// indexCommit embeds and stores a single `git log -p` commit block as one chunk,
// returning true only when the chunk was stored. Every step is best-effort: any
// error skips this commit without failing the history pass.
func (idx *Indexer) indexCommit(ctx context.Context, projectID int, model string, commit []byte) bool {
	if len(commit) > maxChunkChars {
		commit = commit[:maxChunkChars]
	}

	firstLine := bytes.SplitN(commit, []byte("\n"), 2)
	filePath := "git:" + string(bytes.TrimSpace(firstLine[0]))

	fileID, err := idx.db.UpsertFile(ctx, projectID, filePath, fmt.Sprintf("%x", sha256.Sum256(commit)), len(commit))
	if err != nil {
		return false
	}
	if err := idx.db.DeleteChunksForFile(ctx, projectID, fileID, idx.dims); err != nil {
		return false
	}

	embedding, err := idx.embedder.EmbedSingle(ctx, model, string(commit))
	if err != nil {
		return false
	}
	chunk := chunker.Chunk{Content: string(commit), StartLine: 1, EndLine: 1}
	return idx.db.InsertChunks(ctx, projectID, fileID, []chunker.Chunk{chunk}, [][]float32{embedding}, idx.dims) == nil
}

func (idx *Indexer) logf(format string, args ...any) {
	if idx.verbose {
		fmt.Printf(format+"\n", args...)
	}
}

func (idx *Indexer) progress(done, total int, rel string, chunks int) {
	if idx.verbose {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Printf("  [%d/%d] %s (%d chunks) | HeapAlloc: %d MB, Sys: %d MB\n",
			done, total, rel, chunks, m.Alloc/1024/1024, m.Sys/1024/1024)
	} else if done%logEvery == 0 || done == total {
		fmt.Printf("  ...%d/%d files (%d chunks)\n", done, total, chunks)
	}
}

func truncateErr(err error, maxLen int) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
