package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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

const (
	maxFileSize      = 1024 * 1024 // 1MB
	maxChunkChars    = 4000        // ~1000 tokens for bge-m3
	maxChunksPerFile = 32
	logEvery         = 10
	embedBatchSize   = 8
)

type Indexer struct {
	db       *DB
	embedder Embedder
	dims     int
	verbose  bool
	gitMode  bool
	gitSince string
}

type IndexStats struct {
	FilesScanned  int
	FilesIndexed  int
	ChunksCreated int
	Errors        int
}

func NewIndexer(db *DB, emb Embedder, dims int, verbose bool, gitMode bool, gitSince string) *Indexer {
	return &Indexer{db: db, embedder: emb, dims: dims, verbose: verbose, gitMode: gitMode, gitSince: gitSince}
}

func (idx *Indexer) IndexProject(ctx context.Context, projectID int, projectPath, model string, maxFiles int) (*IndexStats, error) {
	stats := &IndexStats{}
	var files []string

	err := filepath.WalkDir(projectPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if d.IsDir() {
			if ignoredDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(projectPath, path)
		if !ShouldIndex(rel) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if maxFiles > 0 && len(files) > maxFiles {
		files = files[:maxFiles]
	}

	stats.FilesScanned = len(files)

	for i, path := range files {
		rel, _ := filepath.Rel(projectPath, path)

		f, err := os.Open(path)
		if err != nil {
			stats.Errors++
			if idx.verbose {
				fmt.Printf("[err] read %s: %s\n", rel, truncateErr(err, 200))
			}
			continue
		}
		content, err := io.ReadAll(io.LimitReader(f, maxFileSize))
		f.Close()
		if err != nil {
			stats.Errors++
			if idx.verbose {
				fmt.Printf("[err] read %s: %s\n", rel, truncateErr(err, 200))
			}
			continue
		}

		if len(strings.TrimSpace(string(content))) == 0 {
			continue
		}

		hash := fmt.Sprintf("%x", sha256.Sum256(content))

		fileID, err := idx.db.UpsertFile(ctx, projectID, rel, hash, len(content))
		if err != nil {
			stats.Errors++
			if idx.verbose {
				fmt.Printf("[err] upsert file %s: %s\n", rel, truncateErr(err, 200))
			}
			continue
		}

		chunks := ChunkFile(rel, content, maxChunkChars)
		if len(chunks) == 0 {
			continue
		}
		if len(chunks) > maxChunksPerFile {
			chunks = chunks[:maxChunksPerFile]
		}

		// Delete old chunks first
		if err := idx.db.DeleteChunksForFile(ctx, projectID, fileID, idx.dims); err != nil {
			stats.Errors++
			if idx.verbose {
				fmt.Printf("[err] delete old chunks %s: %s\n", rel, truncateErr(err, 200))
			}
			continue
		}

		// Process chunks in sub-batches
		fileCtx := ctx
		if IsSensitive(rel) {
			localCtx := WithForceLocal(ctx, true)
			_, err := idx.embedder.ModelInfo(localCtx, model)
			if err != nil {
				if idx.verbose {
					fmt.Printf("  [skip] %s (pulado para proteger privacidade no modelo de nuvem %s)\n", rel, model)
				}
				continue
			}
			fileCtx = localCtx
		}

		for start := 0; start < len(chunks); start += embedBatchSize {
			end := start + embedBatchSize
			if end > len(chunks) {
				end = len(chunks)
			}
			subChunks := chunks[start:end]
			inputs := make([]string, len(subChunks))
			for j, c := range subChunks {
				inputs[j] = c.Content
			}
			embeddings, err := idx.embedWithRetry(fileCtx, model, inputs)
			if err != nil {
				stats.Errors++
				if idx.verbose {
					fmt.Printf("[err] embed %s batch %d-%d: %s\n", rel, start, end, truncateErr(err, 200))
				}
				continue
			}
			if err := idx.db.InsertChunks(ctx, projectID, fileID, subChunks, embeddings, idx.dims); err != nil {
				stats.Errors++
				if idx.verbose {
					fmt.Printf("[err] insert chunks %s batch %d-%d: %s\n", rel, start, end, truncateErr(err, 200))
				}
				continue
			}
			stats.ChunksCreated += len(subChunks)
		}
		stats.FilesIndexed++

		if idx.verbose {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			fmt.Printf("  [%d/%d] Processando %s (%d chunks) | HeapAlloc: %d MB, Sys: %d MB\n",
				i+1, len(files), rel, stats.ChunksCreated, m.Alloc/1024/1024, m.Sys/1024/1024)
		} else if (i+1)%logEvery == 0 || i == len(files)-1 {
			fmt.Printf("  ...%d/%d arquivos (%d chunks)\n", i+1, len(files), stats.ChunksCreated)
		}

		// Hint GC after each file to prevent accumulation of backing arrays
		if i%10 == 0 {
			runtime.GC()
		}
	}

	if idx.gitMode {
		if err := idx.indexGitHistory(ctx, projectID, projectPath, model); err != nil {
			stats.Errors++
			if idx.verbose {
				fmt.Printf("[err] git history: %s\n", truncateErr(err, 200))
			}
		}
	}

	// Marca o projeto como "ready" após indexar arquivos e histórico
	_ = idx.db.UpdateProjectStatus(ctx, projectID, "ready")

	return stats, nil
}

func (idx *Indexer) embedWithRetry(ctx context.Context, model string, inputs []string) ([][]float32, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		embeddings, err := idx.embedder.Embed(ctx, model, inputs...)
		if err == nil {
			return embeddings, nil
		}
		lastErr = err
		// If context was canceled, don't retry
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
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

	// Split by commit boundaries (lines starting with "commit ")
	commits := bytes.Split(out, []byte("\ncommit "))
	for i, commit := range commits {
		if len(commit) == 0 {
			continue
		}
		if i > 0 {
			commit = append([]byte("commit "), commit...)
		}

		// Limit each commit chunk to maxChunkChars
		if len(commit) > maxChunkChars {
			commit = commit[:maxChunkChars]
		}

		// Use first line as identifier
		firstLine := bytes.SplitN(commit, []byte("\n"), 2)
		filePath := "git:" + string(bytes.TrimSpace(firstLine[0]))

		fileID, err := idx.db.UpsertFile(ctx, projectID, filePath, fmt.Sprintf("%x", sha256.Sum256(commit)), len(commit))
		if err != nil {
			continue
		}
		idx.db.DeleteChunksForFile(ctx, projectID, fileID, idx.dims)

		embedding, err := idx.embedder.EmbedSingle(ctx, model, string(commit))
		if err != nil {
			continue
		}

		chunk := Chunk{Content: string(commit)}
		idx.db.InsertChunks(ctx, projectID, fileID, []Chunk{chunk}, [][]float32{embedding}, idx.dims)

		if idx.verbose && i%10 == 0 {
			fmt.Printf("  [git] processed %d commits\n", i+1)
		}
	}
	return nil
}
