package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/pkg/client"
)

const (
	pushMaxFileSize       = 1024 * 1024 // 1 MB, matching indexer's maxFileSize
	pushMaxChunkChars     = 4000
	pushMaxChunks         = 32
	embedBatchSize        = 8
	defaultPushWorkers    = 4
	defaultAsyncBatchSize = 0    // 0 = single batch (no splitting)
	asyncPollInterval     = 2 * time.Second
	asyncPollTimeout      = 30 * time.Minute
)

// pushOptions holds the flag values bound by newPushCmd.
type pushOptions struct {
	projectPath, name, model string
	maxFiles                 int
	batchSize                int
	docs, verbose            bool
	embedLocally, priv       bool
	sync                     bool
	workers                  int
}

func newPushCmd(d *deps) *cobra.Command {
	o := &pushOptions{}
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
			return runPush(cmd, d, o)
		},
	}
	c.Flags().StringVar(&o.projectPath, "project", ".", "Path to the project directory")
	c.Flags().StringVar(&o.name, "name", "", "Project name on the server (default: directory basename)")
	c.Flags().StringVar(&o.model, "model", "bge-m3", "Embedding model name")
	c.Flags().IntVar(&o.maxFiles, "max-files", 0, "Limit number of files (0 = all)")
	c.Flags().IntVar(&o.batchSize, "batch-size", defaultAsyncBatchSize, "Split push into batches of N files (0 = single batch)")
	c.Flags().IntVar(&o.workers, "workers", defaultPushWorkers, "How many files to process concurrently when --embed-locally is active")
	c.Flags().BoolVar(&o.sync, "sync", false, "Use synchronous push (old behavior; for small pushes only)")
	c.Flags().BoolVar(&o.docs, "docs", false, "Treat the path as a document folder, even inside a git repo")
	c.Flags().BoolVar(&o.verbose, "verbose", false, "Show detailed progress")
	c.Flags().BoolVar(&o.embedLocally, "embed-locally", false, "Chunk and embed files locally (client-side) instead of sending raw content")
	c.Flags().BoolVar(&o.priv, "privacy", false, "Force local-only embedding providers (Ollama); requires --embed-locally")
	return c
}

// runPush validates preconditions, scans the project, ensures the server
// project exists, and dispatches to the raw or embed-locally push path.
func runPush(cmd *cobra.Command, d *deps, o *pushOptions) error {
	if err := validatePushPreconditions(d, o); err != nil {
		return err
	}

	d.applyPrivacy(o.priv)
	ctx := cmd.Context()
	cli := d.apiClient()

	tgt := resolveTarget(ctx, o.projectPath, o.docs)
	projectName := o.name
	if projectName == "" {
		projectName = tgt.name
	}

	files, err := scanPushFiles(tgt, o.maxFiles, o.verbose)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil // scanPushFiles already reported "no files"
	}

	if err := ensureServerProject(ctx, cli, projectName, o.model, o.verbose); err != nil {
		return err
	}

	workers := o.workers
	if workers < 1 {
		workers = defaultPushWorkers
	}
	p := &pusher{d: d, cli: cli, tgt: tgt, projectName: projectName, model: o.model, verbose: o.verbose, workers: workers, batchSize: o.batchSize, sync: o.sync}
	if o.embedLocally {
		return p.embedded(ctx, files)
	}
	return p.raw(ctx, files)
}

// validatePushPreconditions rejects flag combinations that push cannot honour.
func validatePushPreconditions(d *deps, o *pushOptions) error {
	switch {
	case !d.remote():
		return fmt.Errorf("no server configured — run \"semidx login <url> --token ...\" first")
	case d.localIndexPath != "":
		return fmt.Errorf("--local is not compatible with push (push requires a server)")
	case d.keywordOnly:
		return fmt.Errorf("--keyword is not compatible with push (use \"semidx index --keyword\" for keyword-only indexing)")
	case o.priv && !o.embedLocally:
		return fmt.Errorf("--privacy requires --embed-locally")
	case systemDirs[filepath.Clean(o.projectPath)]:
		return fmt.Errorf("refusing to push system directory: %s", filepath.Clean(o.projectPath))
	}
	return nil
}

// scanPushFiles scans the target for indexable files. It prints progress and a
// summary; an empty result (with nil error) means there is nothing to push.
func scanPushFiles(tgt indexTarget, maxFiles int, verbose bool) ([]string, error) {
	if verbose {
		fmt.Printf("Scanning %s ...\n", tgt.indexPath)
	}
	files, err := indexing.ScanFiles(tgt.indexPath, maxFiles)
	if err != nil {
		return nil, fmt.Errorf("scan files: %w", err)
	}
	if len(files) == 0 {
		fmt.Println("No indexable files found.")
		return nil, nil
	}
	fmt.Printf("Found %d files to push\n", len(files))
	return files, nil
}

// ensureServerProject creates the project on the server, tolerating an existing
// one (create is idempotent — ok if the project already exists).
func ensureServerProject(ctx context.Context, cli *client.Client, projectName, model string, verbose bool) error {
	if verbose {
		fmt.Printf("Creating project %q on server ...\n", projectName)
	}
	if _, err := cli.CreateProject(ctx, projectName, model, "push", "", ""); err != nil {
		if _, gef := cli.GetProject(ctx, projectName); gef != nil {
			return fmt.Errorf("create project %q: %w", projectName, err)
		}
	}
	return nil
}

// pusher carries the shared state for a single push run so the raw and embedded
// paths need not thread the same arguments through every call.
type pusher struct {
	d           *deps
	cli         *client.Client
	tgt         indexTarget
	projectName string
	model       string
	verbose     bool
	workers     int
	batchSize   int
	sync        bool
}

// rel returns path relative to the push target, falling back to the absolute
// path when a relative form cannot be computed.
func (p *pusher) rel(path string) string {
	rel, err := filepath.Rel(p.tgt.indexPath, path)
	if err != nil {
		return path
	}
	return rel
}

// raw sends raw file content to the server. By default it uses the async batch
// path (202 Accepted + job polling) to avoid timeouts on large pushes. Use
// --sync for the old synchronous behaviour on small pushes; use --batch-size
// to split the push into multiple smaller batches to stay under body limits.
func (p *pusher) raw(ctx context.Context, files []string) error {
	// 1. Hash all files, collect text content.
	hashes, contents, skippedBin := p.hashFiles(files)
	if skippedBin > 0 {
		fmt.Printf("Skipped %d binary files (JARs, images, etc.)\n", skippedBin)
	}

	// 2. Diff with server.
	if p.verbose {
		fmt.Printf("Diffing with server ...\n")
	}
	diff, err := p.cli.FilesDiff(ctx, p.projectName, hashes)
	if err != nil {
		return fmt.Errorf("diff files: %w", err)
	}
	if p.verbose {
		fmt.Printf("  stale: %d, deleted: %d\n", len(diff.Stale), len(diff.Deleted))
	}

	// 3. Build batch — only text files that are stale.
	batch, skippedNoContent := p.buildStaleBatch(diff.Stale, contents)
	if skippedNoContent > 0 {
		fmt.Printf("Skipped %d stale files (binary or unreadable)\n", skippedNoContent)
	}
	if len(batch) == 0 && len(diff.Deleted) == 0 {
		fmt.Println("Everything up to date — nothing to push.")
		return nil
	}

	// 4. Push — async (default) or sync.
	start := time.Now()
	if p.sync {
		return p.pushSync(ctx, batch, diff.Deleted, start)
	}
	return p.pushAsync(ctx, batch, diff.Deleted, start)
}

// pushSync sends files in one synchronous call (the old behaviour). If batchSize
// is set, it splits into multiple synchronous calls.
func (p *pusher) pushSync(ctx context.Context, batch []client.BatchFile, deletions []string, start time.Time) error {
	if p.batchSize > 0 && p.batchSize < len(batch) {
		return p.pushSyncBatched(ctx, batch, deletions, start)
	}
	return p.pushSyncSingle(ctx, batch, deletions, start)
}

// pushSyncSingle sends one synchronous batch.
func (p *pusher) pushSyncSingle(ctx context.Context, batch []client.BatchFile, deletions []string, start time.Time) error {
	fmt.Printf("Pushing %d files (sync), deleting %d ...\n", len(batch), len(deletions))
	resp, err := p.cli.FilesBatch(ctx, p.projectName, batch, deletions)
	if err != nil {
		return fmt.Errorf("push files: %w", err)
	}
	fmt.Printf("Done in %v — indexed: %d, chunks: %d, deleted: %d, errors: %d\n",
		time.Since(start).Round(time.Millisecond),
		resp.Indexed, resp.Chunks, resp.Deleted, resp.Errors)
	return nil
}

// pushSyncBatched splits the batch into multiple synchronous calls. Deletions
// go in the first batch.
func (p *pusher) pushSyncBatched(ctx context.Context, batch []client.BatchFile, deletions []string, start time.Time) error {
	batches := splitBatch(batch, p.batchSize)
	totalBatches := len(batches)
	var totalIndexed, totalChunks, totalDeleted, totalErrors int

	for i, b := range batches {
		del := []string(nil)
		if i == 0 {
			del = deletions
		}
		fmt.Printf("Pushing batch %d/%d (%d files, sync) ...\n", i+1, totalBatches, len(b))
		resp, err := p.cli.FilesBatch(ctx, p.projectName, b, del)
		if err != nil {
			return fmt.Errorf("push batch %d/%d: %w", i+1, totalBatches, err)
		}
		totalIndexed += resp.Indexed
		totalChunks += resp.Chunks
		totalDeleted += resp.Deleted
		totalErrors += resp.Errors
	}

	fmt.Printf("Done in %v — indexed: %d, chunks: %d, deleted: %d, errors: %d\n",
		time.Since(start).Round(time.Millisecond),
		totalIndexed, totalChunks, totalDeleted, totalErrors)
	return nil
}

// pushAsync sends files using the async batch path. By default it sends one
// batch; with --batch-size it splits into multiple batches, each as a separate
// async job. Deletions go in the first batch.
func (p *pusher) pushAsync(ctx context.Context, batch []client.BatchFile, deletions []string, start time.Time) error {
	if p.batchSize > 0 && p.batchSize < len(batch) {
		return p.pushAsyncBatched(ctx, batch, deletions, start)
	}
	return p.pushAsyncSingle(ctx, batch, deletions, start)
}

// pushAsyncSingle enqueues one async batch job and polls for completion.
func (p *pusher) pushAsyncSingle(ctx context.Context, batch []client.BatchFile, deletions []string, start time.Time) error {
	fmt.Printf("Enqueuing %d files (async), deleting %d ...\n", len(batch), len(deletions))
	jobID, err := p.cli.FilesBatchAsync(ctx, p.projectName, batch, deletions)
	if err != nil {
		return fmt.Errorf("enqueue batch: %w", err)
	}
	fmt.Printf("Job %d queued — waiting for completion ...\n", jobID)

	pollCtx, cancel := context.WithTimeout(ctx, asyncPollTimeout)
	defer cancel()

	job, err := p.cli.WaitForJob(pollCtx, jobID, asyncPollInterval)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %v waiting for job %d (job is still running on the server — re-run push to check status)", asyncPollTimeout, jobID)
		}
		return fmt.Errorf("wait for job %d: %w", jobID, err)
	}
	if job.Status == client.JobStatusFailed {
		return fmt.Errorf("job %d failed: %s", jobID, job.Error)
	}
	fmt.Printf("Done in %v — indexed: %d, chunks: %d, deleted: %d, errors: %d\n",
		time.Since(start).Round(time.Millisecond),
		job.FilesIndexed, job.ChunksCreated, job.DeletedFiles, job.ErrorCount)
	return nil
}

// pushAsyncBatched splits the batch into multiple async calls. Each batch is a
// separate job; deletions go in the first batch (so partial failure doesn't
// re-index deleted files — the diff on next push will pick up remaining stale
// files). All jobs are queued, then polled with per-job timeouts.
func (p *pusher) pushAsyncBatched(ctx context.Context, batch []client.BatchFile, deletions []string, start time.Time) error {
	batches := splitBatch(batch, p.batchSize)
	totalBatches := len(batches)
	jobIDs := make([]int, totalBatches)

	// Enqueue all batches.
	for i, b := range batches {
		del := []string(nil)
		if i == 0 {
			del = deletions
		}
		fmt.Printf("Enqueuing batch %d/%d (%d files) ...\n", i+1, totalBatches, len(b))
		jobID, err := p.cli.FilesBatchAsync(ctx, p.projectName, b, del)
		if err != nil {
			return fmt.Errorf("enqueue batch %d/%d: %w (note: %d earlier batch(es) may already be processing on the server)", i+1, totalBatches, err, i)
		}
		jobIDs[i] = jobID
	}

	// Poll all jobs with per-job timeouts (consistent with single-batch semantics).
	fmt.Printf("All %d batch(es) queued — waiting for completion ...\n", totalBatches)
	var totalIndexed, totalChunks, totalDeleted, totalErrors int
	for i, jid := range jobIDs {
		pollCtx, cancel := context.WithTimeout(ctx, asyncPollTimeout)
		job, err := p.cli.WaitForJob(pollCtx, jid, asyncPollInterval)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("timed out after %v waiting for batch %d/%d (job %d — still running on server; re-run push to check status)", asyncPollTimeout, i+1, totalBatches, jid)
			}
			return fmt.Errorf("wait for batch %d/%d (job %d): %w", i+1, totalBatches, jid, err)
		}
		if job.Status == client.JobStatusFailed {
			return fmt.Errorf("batch %d/%d (job %d) failed: %s", i+1, totalBatches, jid, job.Error)
		}
		totalIndexed += job.FilesIndexed
		totalChunks += job.ChunksCreated
		totalDeleted += job.DeletedFiles
		totalErrors += job.ErrorCount
		if p.verbose {
			fmt.Printf("  batch %d/%d done — indexed: %d, chunks: %d\n", i+1, totalBatches, job.FilesIndexed, job.ChunksCreated)
		}
	}

	fmt.Printf("Done in %v — indexed: %d, chunks: %d, deleted: %d, errors: %d\n",
		time.Since(start).Round(time.Millisecond),
		totalIndexed, totalChunks, totalDeleted, totalErrors)
	return nil
}

// splitBatch divides a batch of files into chunks of up to chunkSize elements.
func splitBatch(batch []client.BatchFile, chunkSize int) [][]client.BatchFile {
	if chunkSize <= 0 || chunkSize >= len(batch) {
		return [][]client.BatchFile{batch}
	}
	var chunks [][]client.BatchFile
	for i := 0; i < len(batch); i += chunkSize {
		end := i + chunkSize
		if end > len(batch) {
			end = len(batch)
		}
		chunks = append(chunks, batch[i:end])
	}
	return chunks
}

// hashFiles hashes every file and, for text files, collects their content.
// It returns the path→hash and path→content maps plus the count of skipped
// binary files. Both maps are keyed by the path relative to the push target.
func (p *pusher) hashFiles(files []string) (hashes, contents map[string]string, skippedBin int) {
	hashes = make(map[string]string, len(files))
	contents = make(map[string]string, len(files))
	for i, path := range files {
		if p.verbose && i%100 == 0 {
			fmt.Printf("  hashing %d/%d ...\n", i+1, len(files))
		}
		rel := p.rel(path)
		hash, content, isText, rErr := readAndHash(path)
		if rErr != nil {
			if p.verbose {
				fmt.Fprintf(os.Stderr, "[warn] %s: %v\n", rel, rErr)
			}
			continue
		}
		hashes[rel] = hash
		if isText {
			contents[rel] = content
		} else {
			skippedBin++
			if p.verbose {
				fmt.Fprintf(os.Stderr, "[warn] skipping binary: %s\n", rel)
			}
		}
	}
	return hashes, contents, skippedBin
}

// buildStaleBatch turns the server's stale-path list into a batch of text
// files, skipping any path whose content was not collected (binary/unreadable).
func (p *pusher) buildStaleBatch(stale []string, contents map[string]string) (batch []client.BatchFile, skippedNoContent int) {
	for _, path := range stale {
		content, ok := contents[path]
		if !ok {
			skippedNoContent++
			if p.verbose {
				fmt.Fprintf(os.Stderr, "[warn] skipping (no content): %s\n", path)
			}
			continue
		}
		batch = append(batch, client.BatchFile{Path: path, Content: content})
	}
	return batch, skippedNoContent
}

// embedResult is the outcome of embedding a single file in the embed-locally path.
type embedResult struct {
	file       client.BatchFile
	include    bool // add file to the push batch
	skippedBin bool // skipped as a binary file
	fellBack   bool // embedding failed; file included as raw for server-side embed
}

// embedStats accumulates per-file outcomes across the concurrent embed run.
type embedStats struct {
	skippedBin  int
	embedFailed int
	fallback    int
}

// embedAccumulator collects embed results from concurrent workers behind a mutex.
type embedAccumulator struct {
	mu    sync.Mutex
	batch []client.BatchFile
	stats embedStats
}

func (a *embedAccumulator) add(res embedResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if res.skippedBin {
		a.stats.skippedBin++
	}
	if res.fellBack {
		a.stats.embedFailed++
		a.stats.fallback++
	}
	if res.include {
		a.batch = append(a.batch, res.file)
	}
}

// embedded chunks and embeds files locally in parallel, then pushes
// pre-computed embeddings to the server. Falls back to raw push for individual
// files when embedding fails, or wholesale when the provider is unavailable.
func (p *pusher) embedded(ctx context.Context, files []string) error {
	// 1. Verify local embedding provider is reachable.
	if _, err := p.d.emb.ModelInfo(ctx, p.model); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] local embedding provider not available for model %s: %v\n", p.model, err)
		fmt.Fprintf(os.Stderr, "       falling back to raw push (server will embed).\n")
		return p.raw(ctx, files)
	}
	fmt.Printf("Embedding locally with model %s (workers: %d, skipping diff)\n", p.model, p.workers)

	// 2. Read files, chunk, embed concurrently — same pattern as the indexer.
	batch, stats := p.embedAll(ctx, files)
	if stats.skippedBin > 0 {
		fmt.Printf("Skipped %d binary files (JARs, images, etc.)\n", stats.skippedBin)
	}
	if stats.embedFailed > 0 {
		fmt.Printf("Embedding failed for %d files (%d fell back to raw push)\n", stats.embedFailed, stats.fallback)
	}
	if len(batch) == 0 {
		fmt.Println("No text files to push.")
		return nil
	}

	// 3. Push all files (no diff — we embed everything to ensure embeddings are present).
	if p.sync {
		return p.pushSync(ctx, batch, nil, time.Now())
	}
	return p.pushAsync(ctx, batch, nil, time.Now())
}

// embedAll runs the concurrent read/chunk/embed pipeline over files and returns
// the assembled push batch plus the aggregated per-file statistics.
func (p *pusher) embedAll(ctx context.Context, files []string) ([]client.BatchFile, embedStats) {
	var (
		acc       embedAccumulator
		processed atomic.Int64
		g         errgroup.Group
	)
	g.SetLimit(p.workers)
	for _, path := range files {
		path := path // capture for goroutine
		g.Go(func() error {
			if n := processed.Add(1); p.verbose && n%10 == 0 {
				fmt.Printf("  embedding %d/%d ...\n", n, len(files))
			}
			acc.add(p.embedOneFile(ctx, path))
			return nil
		})
	}
	_ = g.Wait() // embedOneFile never returns an error; failures are captured in embedResult
	return acc.batch, acc.stats
}

// embedOneFile reads, chunks and embeds a single file, returning what should be
// added to the push batch. On embedding failure it returns the file as a raw
// fallback so the server can embed it; unreadable files return an empty result.
func (p *pusher) embedOneFile(ctx context.Context, path string) embedResult {
	rel := p.rel(path)

	content, isText, rErr := readFileContent(path)
	if rErr != nil {
		if p.verbose {
			fmt.Fprintf(os.Stderr, "[warn] %s: %v\n", rel, rErr)
		}
		return embedResult{}
	}
	if !isText {
		if p.verbose {
			fmt.Fprintf(os.Stderr, "[warn] skipping binary: %s\n", rel)
		}
		return embedResult{skippedBin: true}
	}

	chunks := chunker.ChunkFile(path, []byte(content), pushMaxChunkChars)
	if len(chunks) > pushMaxChunks {
		if p.verbose {
			fmt.Fprintf(os.Stderr, "[warn] %s: truncated %d chunks to %d\n", rel, len(chunks), pushMaxChunks)
		}
		chunks = chunks[:pushMaxChunks]
	}
	if len(chunks) == 0 {
		return embedResult{}
	}

	chunkTexts := make([]string, len(chunks))
	for i, ch := range chunks {
		chunkTexts[i] = ch.Content
	}
	embeddings, embErr := embedChunks(ctx, p.d, p.model, rel, chunkTexts)
	if embErr != nil {
		if p.verbose {
			fmt.Fprintf(os.Stderr, "[warn] embedding failed for %s, falling back to raw push: %v\n", rel, embErr)
		}
		return embedResult{file: client.BatchFile{Path: rel, Content: content}, include: true, fellBack: true}
	}

	fileChunks := make([]client.EmbeddedChunk, len(chunks))
	for j, ch := range chunks {
		fileChunks[j] = client.EmbeddedChunk{
			StartLine: ch.StartLine,
			EndLine:   ch.EndLine,
			Content:   ch.Content,
			Embedding: embeddings[j],
		}
	}
	return embedResult{file: client.BatchFile{Path: rel, Content: content, Chunks: fileChunks}, include: true}
}

// embedChunks embeds a batch of texts, respecting privacy routing for sensitive
// files. It splits into sub-batches of embedBatchSize to match the indexer's
// batching strategy.
func embedChunks(ctx context.Context, d *deps, model, rel string, texts []string) ([][]float32, error) {
	embedCtx := ctx
	if privacy.IsSensitive(rel) {
		embedCtx = embed.WithForceLocal(ctx, true)
	}
	var all [][]float32
	for i := 0; i < len(texts); i += embedBatchSize {
		end := i + embedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]

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
