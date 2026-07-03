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
	keywordOnly bool // when true, store text-only (no embeddings) for keyword search
}

// IndexStats summarizes an indexing run.
type IndexStats struct {
	FilesScanned  int
	FilesIndexed  int
	FilesSkipped  int // unchanged since last index (incremental)
	ChunksCreated int
	Errors        int
}

// fileOutcome is how indexFile handled a single file.
type fileOutcome int

const (
	outcomeIndexed          fileOutcome = iota // (re)embedded and stored
	outcomeSkippedEmpty                        // empty or produced no chunks
	outcomeSkippedUnchanged                    // already indexed with the same hash
)

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

// IndexProject scans projectPath, indexes each eligible file, optionally indexes
// git history, and marks the project ready.
func (idx *Indexer) IndexProject(ctx context.Context, projectID int, projectPath, model string, maxFiles int) (*IndexStats, error) {
	stats := &IndexStats{}

	files, err := scanFiles(projectPath, maxFiles)
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
			created, softErrs, outcome, ferr := idx.indexFile(ctx, projectID, path, rel, model)

			mu.Lock()
			stats.Errors += softErrs
			switch {
			case ferr != nil:
				stats.Errors++
				idx.logf("[err] %s: %s", rel, truncateErr(ferr, 200))
			case outcome == outcomeIndexed:
				stats.ChunksCreated += created
				stats.FilesIndexed++
			case outcome == outcomeSkippedUnchanged:
				stats.FilesSkipped++
			}
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

	if idx.gitMode {
		if err := idx.indexGitHistory(ctx, projectID, projectPath, model); err != nil {
			stats.Errors++
			idx.logf("[err] git history: %s", truncateErr(err, 200))
		}
	}

	_ = idx.db.UpdateProjectStatus(ctx, projectID, "ready")
	return stats, nil
}

// scanFiles walks projectPath, skipping ignored dirs and non-indexable files,
// and caps the result at maxFiles (0 = unlimited).
func scanFiles(projectPath string, maxFiles int) ([]string, error) {
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
func (idx *Indexer) indexFile(ctx context.Context, projectID int, path, rel, model string) (created, softErrs int, outcome fileOutcome, hardErr error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, err
	}
	content, err := io.ReadAll(io.LimitReader(f, maxFileSize))
	_ = f.Close()
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, err
	}
	return idx.indexContent(ctx, projectID, rel, model, content)
}

// IndexContent indexes one file's in-memory content (used by the push files API)
// and returns the number of chunks created.
func (idx *Indexer) IndexContent(ctx context.Context, projectID int, rel, model string, content []byte) (int, error) {
	created, _, _, err := idx.indexContent(ctx, projectID, rel, model, content)
	return created, err
}

// indexContent chunks, embeds and stores one file's content (shared by disk and
// push paths).
func (idx *Indexer) indexContent(ctx context.Context, projectID int, rel, model string, content []byte) (created, softErrs int, outcome fileOutcome, hardErr error) {
	if len(strings.TrimSpace(string(content))) == 0 {
		return 0, 0, outcomeSkippedEmpty, nil
	}
	if len(content) > maxFileSize {
		content = content[:maxFileSize]
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(content))

	// Incremental: skip files already indexed with this exact content. Hashing
	// the RAW bytes means an unchanged document is skipped without paying the
	// extraction cost.
	if upToDate, err := idx.db.FileUpToDate(ctx, projectID, rel, hash, idx.dims); err == nil && upToDate {
		return 0, 0, outcomeSkippedUnchanged, nil
	}

	// Documents (PDF/Office/HTML) are converted to text before chunking; code and
	// plain-text files pass through unchanged. Extraction runs only for new or
	// changed documents (after the incremental check above).
	if extract.Supported(rel) {
		text, err := extract.Extract(rel, content)
		switch {
		case errors.Is(err, extract.ErrEncrypted):
			return 0, 0, outcomeSkippedEmpty, nil // password-protected: expected skip, not an error
		case err != nil:
			return 0, 1, outcomeSkippedEmpty, nil // malformed document: soft error, never fatal
		}
		content = []byte(text)
		if len(strings.TrimSpace(text)) == 0 {
			return 0, 0, outcomeSkippedEmpty, nil // e.g. a scanned, text-less PDF
		}
	}

	fileID, err := idx.db.UpsertFile(ctx, projectID, rel, hash, len(content))
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, err
	}

	chunks := chunker.ChunkFile(rel, content, maxChunkChars)
	if len(chunks) == 0 {
		return 0, 0, outcomeSkippedEmpty, nil
	}
	if len(chunks) > maxChunksPerFile {
		chunks = chunks[:maxChunksPerFile]
	}

	if err := idx.db.DeleteChunksForFile(ctx, projectID, fileID, idx.dims); err != nil {
		return 0, 0, outcomeSkippedEmpty, err
	}

	// Keyword-only mode: no embedding provider at all — store the chunks as text
	// so they stay searchable by keyword (FTS/ILIKE) without any model.
	if idx.keywordOnly {
		if err := idx.db.InsertChunksTextOnly(ctx, projectID, fileID, chunks, idx.dims); err != nil {
			return 0, 0, outcomeSkippedEmpty, err
		}
		return len(chunks), 0, outcomeIndexed, nil
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
				return 0, 0, outcomeSkippedEmpty, err
			}
			idx.logf("  [local-text] %s (sensitive; stored text-only, no cloud embedding)", rel)
			return len(chunks), 0, outcomeIndexed, nil
		}
		embedCtx = localCtx
	}

	created, softErrs = idx.embedAndInsert(embedCtx, projectID, fileID, chunks, model, rel)
	return created, softErrs, outcomeIndexed, nil
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
	cmd := exec.Command("git", "-C", projectPath, "log", "-p", "--since="+idx.gitSince)
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
		if len(commit) > maxChunkChars {
			commit = commit[:maxChunkChars]
		}

		firstLine := bytes.SplitN(commit, []byte("\n"), 2)
		filePath := "git:" + string(bytes.TrimSpace(firstLine[0]))

		fileID, err := idx.db.UpsertFile(ctx, projectID, filePath, fmt.Sprintf("%x", sha256.Sum256(commit)), len(commit))
		if err != nil {
			continue
		}
		if err := idx.db.DeleteChunksForFile(ctx, projectID, fileID, idx.dims); err != nil {
			continue
		}

		embedding, err := idx.embedder.EmbedSingle(ctx, model, string(commit))
		if err != nil {
			continue
		}
		chunk := chunker.Chunk{Content: string(commit), StartLine: 1, EndLine: 1}
		if err := idx.db.InsertChunks(ctx, projectID, fileID, []chunker.Chunk{chunk}, [][]float32{embedding}, idx.dims); err != nil {
			continue
		}

		if idx.verbose && i%10 == 0 {
			fmt.Printf("  [git] processed %d commits\n", i+1)
		}
	}
	return nil
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
		fmt.Printf("  ...%d/%d arquivos (%d chunks)\n", done, total, chunks)
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
