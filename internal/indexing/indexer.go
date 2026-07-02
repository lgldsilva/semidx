// Package indexing walks a project, chunks each file and stores embeddings,
// routing sensitive files away from cloud providers. It depends only on the
// store.Store and embed.Embedder interfaces, so the pipeline is unit-testable
// with fakes.
package indexing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/store"
)

const (
	maxFileSize      = 1024 * 1024 // 1MB
	maxChunkChars    = 4000        // ~1000 tokens for bge-m3
	maxChunksPerFile = 32
	logEvery         = 10
	embedBatchSize   = 8
)

// Indexer indexes a project into a Store using an Embedder.
type Indexer struct {
	db       store.Store
	embedder embed.Embedder
	dims     int
	verbose  bool
	gitMode  bool
	gitSince string
}

// IndexStats summarizes an indexing run.
type IndexStats struct {
	FilesScanned  int
	FilesIndexed  int
	ChunksCreated int
	Errors        int
}

// NewIndexer wires an Indexer. dims is the embedding dimension of model.
func NewIndexer(db store.Store, emb embed.Embedder, dims int, verbose, gitMode bool, gitSince string) *Indexer {
	return &Indexer{db: db, embedder: emb, dims: dims, verbose: verbose, gitMode: gitMode, gitSince: gitSince}
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

	for i, path := range files {
		// Stop promptly on Ctrl-C / SIGTERM, returning what we have so far.
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		rel, _ := filepath.Rel(projectPath, path)

		created, softErrs, ok, err := idx.indexFile(ctx, projectID, path, rel, model)
		stats.Errors += softErrs
		if err != nil {
			stats.Errors++
			idx.logf("[err] %s: %s", rel, truncateErr(err, 200))
			continue
		}
		if !ok {
			continue
		}
		stats.ChunksCreated += created
		stats.FilesIndexed++
		idx.progress(i, len(files), rel, stats)
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
		if chunker.ShouldIndex(rel) {
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

// indexFile reads, chunks and stores one file. ok is false for empty or
// chunk-less files (skipped silently); hardErr signals a read/upsert/delete
// failure (the whole file is dropped); softErrs counts failed embed sub-batches.
func (idx *Indexer) indexFile(ctx context.Context, projectID int, path, rel, model string) (created, softErrs int, ok bool, hardErr error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false, err
	}
	content, err := io.ReadAll(io.LimitReader(f, maxFileSize))
	_ = f.Close()
	if err != nil {
		return 0, 0, false, err
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return 0, 0, false, nil
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	fileID, err := idx.db.UpsertFile(ctx, projectID, rel, hash, len(content))
	if err != nil {
		return 0, 0, false, err
	}

	chunks := chunker.ChunkFile(rel, content, maxChunkChars)
	if len(chunks) == 0 {
		return 0, 0, false, nil
	}
	if len(chunks) > maxChunksPerFile {
		chunks = chunks[:maxChunksPerFile]
	}

	if err := idx.db.DeleteChunksForFile(ctx, projectID, fileID, idx.dims); err != nil {
		return 0, 0, false, err
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
				return 0, 0, false, err
			}
			idx.logf("  [local-text] %s (sensitive; stored text-only, no cloud embedding)", rel)
			return len(chunks), 0, true, nil
		}
		embedCtx = localCtx
	}

	created, softErrs = idx.embedAndInsert(embedCtx, projectID, fileID, chunks, model, rel)
	return created, softErrs, true, nil
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

func (idx *Indexer) progress(i, total int, rel string, stats *IndexStats) {
	if idx.verbose {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Printf("  [%d/%d] %s (%d chunks) | HeapAlloc: %d MB, Sys: %d MB\n",
			i+1, total, rel, stats.ChunksCreated, m.Alloc/1024/1024, m.Sys/1024/1024)
	} else if (i+1)%logEvery == 0 || i == total-1 {
		fmt.Printf("  ...%d/%d arquivos (%d chunks)\n", i+1, total, stats.ChunksCreated)
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
