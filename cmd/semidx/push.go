package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/pkg/client"
)

const (
	pushMaxFileSize    = 1024 * 1024 // 1 MB, matching indexer's maxFileSize
	pushMaxChunkChars  = 4000
	pushMaxChunks      = 32
	embedBatchSize     = 8
	defaultPushWorkers = 4
)

func newPushCmd(d *deps) *cobra.Command {
	var (
		projectPath, name, model string
		maxFiles                 int
		docs, verbose            bool
		embedLocally, priv       bool
		workers                  int
	)
	c := &cobra.Command{
		Use:   "push",
		Short: "Push local files to a semidx server for indexing",
		Long: `Walk a local directory and push its files to a configured semidx server
for remote indexing.

RAW mode (default): raw file content is sent to the server, which chunks and
embeds using its own credentials. Best when no local embedding provider (Ollama,
Gemini, etc.) is available.

EMBED-LOCALLY mode (--embed-locally): files are chunked and embedded on the
client (using local Ollama or any configured provider), and pre-computed
embeddings are sent to the server. This offloads embedding cost from the server
and enables faster pushes when a local provider is available. Falls back to raw
push for individual files when embedding fails.

Requires a logged-in server: "semidx login <url> --token ..."

Binary files (JARs, images, compiled objects) are skipped — the push API
transports content as JSON strings, which cannot represent arbitrary bytes.
Use "semidx repo add" to index a full git repository server-side instead.`,
		Example: `  semidx push --project .                        # raw push (server embeds)
  semidx push --project . --embed-locally          # client embeds via Ollama
  semidx push --project . --embed-locally --privacy # force local for .env files
  semidx push --project ./docs --docs --name my-docs
  semidx push --project . --max-files 100          # limit to first 100 files`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !d.remote() {
				return fmt.Errorf("no server configured — run \"semidx login <url> --token ...\" first")
			}
			if d.cfg.LocalIndexPath != "" {
				return fmt.Errorf("--local is not compatible with push (push requires a server)")
			}
			if d.cfg.KeywordOnly {
				return fmt.Errorf("--keyword is not compatible with push (use \"semidx index --keyword\" for keyword-only indexing)")
			}
			if priv && !embedLocally {
				return fmt.Errorf("--privacy requires --embed-locally")
			}
			if systemDirs[filepath.Clean(projectPath)] {
				return fmt.Errorf("refusing to push system directory: %s", filepath.Clean(projectPath))
			}

			d.applyPrivacy(priv)
			ctx := cmd.Context()
			cli := d.apiClient()

			tgt := resolveTarget(ctx, projectPath, docs)
			projectName := name
			if projectName == "" {
				projectName = tgt.name
			}

			// 1. Scan files.
			if verbose {
				fmt.Printf("Scanning %s ...\n", tgt.indexPath)
			}
			files, err := indexing.ScanFiles(tgt.indexPath, maxFiles)
			if err != nil {
				return fmt.Errorf("scan files: %w", err)
			}
			if len(files) == 0 {
				fmt.Println("No indexable files found.")
				return nil
			}
			fmt.Printf("Found %d files to push\n", len(files))

			// 2. Create project on the server (idempotent — ok if already exists).
			if verbose {
				fmt.Printf("Creating project %q on server ...\n", projectName)
			}
			_, err = cli.CreateProject(ctx, projectName, model, "push", "", "")
			if err != nil {
				if _, gef := cli.GetProject(ctx, projectName); gef != nil {
					return fmt.Errorf("create project %q: %w", projectName, err)
				}
			}

			// 3. Push — branch on mode.
			if workers < 1 {
				workers = defaultPushWorkers
			}
			if embedLocally {
				return pushEmbedded(ctx, d, cli, tgt, projectName, model, files, verbose, workers)
			}
			return pushRaw(ctx, cli, tgt, projectName, model, files, verbose)
		},
	}
	c.Flags().StringVar(&projectPath, "project", ".", "Path to the project directory")
	c.Flags().StringVar(&name, "name", "", "Project name on the server (default: directory basename)")
	c.Flags().StringVar(&model, "model", "bge-m3", "Embedding model name")
	c.Flags().IntVar(&maxFiles, "max-files", 0, "Limit number of files (0 = all)")
	c.Flags().IntVar(&workers, "workers", defaultPushWorkers, "How many files to process concurrently when --embed-locally is active")
	c.Flags().BoolVar(&docs, "docs", false, "Treat the path as a document folder, even inside a git repo")
	c.Flags().BoolVar(&verbose, "verbose", false, "Show detailed progress")
	c.Flags().BoolVar(&embedLocally, "embed-locally", false, "Chunk and embed files locally (client-side) instead of sending raw content")
	c.Flags().BoolVar(&priv, "privacy", false, "Force local-only embedding providers (Ollama); requires --embed-locally")
	return c
}

// pushRaw sends raw file content to the server (current/default behavior). Only
// text files that are stale (different from server) are pushed.
func pushRaw(ctx context.Context, cli *client.Client, tgt indexTarget, projectName, model string, files []string, verbose bool) error {
	// 1. Hash all files, collect text content.
	hashes := make(map[string]string, len(files))
	contents := make(map[string]string, len(files))
	var skippedBin int

	for i, path := range files {
		if verbose && i%100 == 0 {
			fmt.Printf("  hashing %d/%d ...\n", i+1, len(files))
		}
		rel, err := filepath.Rel(tgt.indexPath, path)
		if err != nil {
			rel = path
		}
		hash, content, isText, rErr := readAndHash(path)
		if rErr != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "[warn] %s: %v\n", rel, rErr)
			}
			continue
		}
		hashes[rel] = hash
		if isText {
			contents[rel] = content
		} else {
			skippedBin++
			if verbose {
				fmt.Fprintf(os.Stderr, "[warn] skipping binary: %s\n", rel)
			}
		}
	}
	if skippedBin > 0 {
		fmt.Printf("Skipped %d binary files (JARs, images, etc.)\n", skippedBin)
	}

	// 2. Diff with server.
	if verbose {
		fmt.Printf("Diffing with server ...\n")
	}
	diff, err := cli.FilesDiff(ctx, projectName, hashes)
	if err != nil {
		return fmt.Errorf("diff files: %w", err)
	}
	if verbose {
		fmt.Printf("  stale: %d, deleted: %d\n", len(diff.Stale), len(diff.Deleted))
	}

	// 3. Build batch — only text files that are stale.
	var batch []client.BatchFile
	var skippedNoContent int
	for _, p := range diff.Stale {
		content, ok := contents[p]
		if !ok {
			skippedNoContent++
			if verbose {
				fmt.Fprintf(os.Stderr, "[warn] skipping (no content): %s\n", p)
			}
			continue
		}
		batch = append(batch, client.BatchFile{Path: p, Content: content})
	}
	if skippedNoContent > 0 {
		fmt.Printf("Skipped %d stale files (binary or unreadable)\n", skippedNoContent)
	}

	if len(batch) == 0 && len(diff.Deleted) == 0 {
		fmt.Println("Everything up to date — nothing to push.")
		return nil
	}

	// 4. Push.
	start := time.Now()
	fmt.Printf("Pushing %d files, deleting %d ...\n", len(batch), len(diff.Deleted))
	resp, err := cli.FilesBatch(ctx, projectName, batch, diff.Deleted)
	if err != nil {
		return fmt.Errorf("push files: %w", err)
	}
	fmt.Printf("Done in %v — indexed: %d, chunks: %d, deleted: %d, errors: %d\n",
		time.Since(start).Round(time.Millisecond),
		resp.Indexed, resp.Chunks, resp.Deleted, resp.Errors)
	return nil
}

// pushEmbedded chunks and embeds files locally in parallel, then pushes
// pre-computed embeddings to the server. Falls back to raw push for individual
// files when embedding fails.
func pushEmbedded(ctx context.Context, d *deps, cli *client.Client, tgt indexTarget, projectName, model string, files []string, verbose bool, workers int) error {
	// 1. Verify local embedding provider is reachable.
	if _, err := d.emb.ModelInfo(ctx, model); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] local embedding provider not available for model %s: %v\n", model, err)
		fmt.Fprintf(os.Stderr, "       falling back to raw push (server will embed).\n")
		return pushRaw(ctx, cli, tgt, projectName, model, files, verbose)
	}
	fmt.Printf("Embedding locally with model %s (workers: %d, skipping diff)\n", model, workers)

	// 2. Read files, chunk, embed concurrently — same pattern as the indexer.
	var (
		mu                                sync.Mutex
		batch                             []client.BatchFile
		skippedBin, embedFailed, fallback int
		processed                         int
		g                                 errgroup.Group
	)
	g.SetLimit(workers)
	start := time.Now()

	for i, path := range files {
		i, path := i, path // capture for goroutine
		g.Go(func() error {
			rel, relErr := filepath.Rel(tgt.indexPath, path)
			if relErr != nil {
				rel = path
			}

			mu.Lock()
			processed++
			n := processed
			mu.Unlock()
			if verbose && n%10 == 0 {
				fmt.Printf("  embedding %d/%d ...\n", n, len(files))
			}

			// Read file.
			content, isText, rErr := readFileContent(path)
			if rErr != nil {
				if verbose {
					fmt.Fprintf(os.Stderr, "[warn] %s: %v\n", rel, rErr)
				}
				return nil
			}
			if !isText {
				mu.Lock()
				skippedBin++
				mu.Unlock()
				if verbose {
					fmt.Fprintf(os.Stderr, "[warn] skipping binary: %s\n", rel)
				}
				return nil
			}

			// Chunk.
			chunks := chunker.ChunkFile(path, []byte(content), pushMaxChunkChars)
			if len(chunks) > pushMaxChunks {
				if verbose {
					fmt.Fprintf(os.Stderr, "[warn] %s: truncated %d chunks to %d\n", rel, len(chunks), pushMaxChunks)
				}
				chunks = chunks[:pushMaxChunks]
			}
			if len(chunks) == 0 {
				return nil
			}

			// Embed chunks in batches.
			var chunkTexts []string
			for _, ch := range chunks {
				chunkTexts = append(chunkTexts, ch.Content)
			}
			embeddings, embErr := embedChunks(ctx, d, model, chunkTexts)
			if embErr != nil {
				mu.Lock()
				embedFailed++
				fallback++
				mu.Unlock()
				if verbose {
					fmt.Fprintf(os.Stderr, "[warn] embedding failed for %s, falling back to raw push: %v\n", rel, embErr)
				}
				mu.Lock()
				batch = append(batch, client.BatchFile{Path: rel, Content: content})
				mu.Unlock()
				return nil
			}

			// Build pre-computed chunks.
			fileChunks := make([]client.EmbeddedChunk, len(chunks))
			for j, ch := range chunks {
				fileChunks[j] = client.EmbeddedChunk{
					StartLine: ch.StartLine,
					EndLine:   ch.EndLine,
					Content:   ch.Content,
					Embedding: embeddings[j],
				}
			}
			mu.Lock()
			batch = append(batch, client.BatchFile{Path: rel, Content: content, Chunks: fileChunks})
			mu.Unlock()
			return nil
		})
		_ = i // suppress unused variable warning
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("embed files: %w", err)
	}

	if skippedBin > 0 {
		fmt.Printf("Skipped %d binary files (JARs, images, etc.)\n", skippedBin)
	}
	if embedFailed > 0 {
		fmt.Printf("Embedding failed for %d files (%d fell back to raw push)\n", embedFailed, fallback)
	}

	if len(batch) == 0 {
		fmt.Println("No text files to push.")
		return nil
	}

	// 3. Push all files (no diff — we embed everything to ensure embeddings are present).
	fmt.Printf("Pushing %d files (pre-embedded) ...\n", len(batch))
	resp, err := cli.FilesBatch(ctx, projectName, batch, nil)
	if err != nil {
		return fmt.Errorf("push files: %w", err)
	}
	fmt.Printf("Done in %v — indexed: %d, chunks: %d, errors: %d\n",
		time.Since(start).Round(time.Millisecond),
		resp.Indexed, resp.Chunks, resp.Errors)
	return nil
}

// embedChunks embeds a batch of texts, respecting privacy mode for sensitive
// files. It splits into sub-batches of embedBatchSize to match the indexer's
// batching strategy.
func embedChunks(ctx context.Context, d *deps, model string, texts []string) ([][]float32, error) {
	var all [][]float32
	for i := 0; i < len(texts); i += embedBatchSize {
		end := i + embedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

		embedCtx := ctx
		embeddings, err := d.emb.Embed(embedCtx, model, batch...)
		if err != nil {
			return nil, fmt.Errorf("embed batch %d: %w", i/embedBatchSize, err)
		}
		all = append(all, embeddings...)
	}
	return all, nil
}

// readFileContent reads a file and returns its content as a string (up to
// pushMaxFileSize), along with an isText flag. Binary files return isText=false.
func readFileContent(path string) (content string, isText bool, err error) {
	// #nosec G304 -- path comes from ScanFiles which walks a user-specified project directory.
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()

	lr := io.LimitReader(f, pushMaxFileSize)
	data, err := io.ReadAll(lr)
	if err != nil {
		return "", false, err
	}

	if utf8.Valid(data) && !containsNull(data) {
		return string(data), true, nil
	}
	return "", false, nil
}

// readAndHash reads up to pushMaxFileSize bytes from a file, computes its SHA-256
// hex digest, and returns the content as a string. isText is false when the content
// is not valid UTF-8 (binary files that would be corrupted by JSON encoding).
func readAndHash(path string) (hashHex, content string, isText bool, err error) {
	content, isText, err = readFileContent(path)
	if err != nil {
		return "", "", false, err
	}
	if !isText {
		// Still compute hash even for binary files (needed for diff).
		dataRead, rErr := readRawFileData(path)
		if rErr != nil {
			return "", "", false, rErr
		}
		h := sha256.Sum256(dataRead)
		return fmt.Sprintf("%x", h), "", false, nil
	}
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h), content, true, nil
}

// readRawFileData reads up to pushMaxFileSize bytes from a file. Used for hashing
// binary files where we can't store the content as a string.
func readRawFileData(path string) ([]byte, error) {
	// #nosec G304 -- path comes from ScanFiles which walks a user-specified project directory.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	lr := io.LimitReader(f, pushMaxFileSize)
	return io.ReadAll(lr)
}

// containsNull reports whether b contains a NUL byte, which is a strong signal
// the file is binary regardless of UTF-8 validity.
func containsNull(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}
