// Package indexing walks a project, chunks each file and stores embeddings,
// routing sensitive files away from cloud providers. It depends only on the
// store.IndexStore and embed.Embedder interfaces, so the pipeline is
// unit-testable with fakes and can run against a standalone local store.
package indexing

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	sym "github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/extract"
	"github.com/lgldsilva/semidx/internal/gitenv"
	si "github.com/lgldsilva/semidx/internal/imports"
	"github.com/lgldsilva/semidx/internal/observ"
	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/secrets"
	"github.com/lgldsilva/semidx/internal/store"
)

const (
	logEvery            = 10
	defaultIndexWorkers = 4
)

// Indexer indexes a project into an IndexStore using an Embedder.
type Indexer struct {
	db                  store.IndexStore
	embedder            embed.Embedder
	dims                int
	workers             int
	embedBatchSize      int
	maxFileSize         int // files larger than this are truncated; 0 = use default
	maxChunksPerFile    int // cap on chunks per file; 0 = use default
	maxChunksPerProject int // cap on total chunks per project; 0 = unlimited
	maxFilesPerProject  int // cap on files scanned per index run; 0 = unlimited
	maxChunkChars       int // max characters per chunk (~1000 tokens for bge-m3)
	log                 *slog.Logger
	verbose             bool
	gitMode             bool
	gitSince            string
	keywordOnly         bool   // when true, store text-only (no embeddings) for keyword search
	noSymbols           bool   // when true, skip symbol enrichment (even when embedding)
	modulePath          string // Go module path from go.mod (for import-dependency extraction)
	worktree            string // when set, record this worktree's manifest + prune after indexing

	// Secret scan: when enabled, every file's content is scanned with gitleaks
	// before chunking. Files with detected secrets are stored text-only (no
	// embedding) when SecretBlockEmbedding is true. env: SEMIDX_SECRET_SCAN,
	// SEMIDX_SECRET_BLOCK_EMBEDDING.
	secretDetector       *secrets.Detector
	secretScanEnabled    bool
	secretBlockEmbedding bool
	onProgress           IndexProgressFunc

	// mem throttling for the verbose progress path: ReadMemStats triggers a
	// stop-the-world, so we cache results and refresh at most once per 10s.
	memMu      sync.Mutex
	memAt      time.Time
	lastHeapMB uint64
	lastSysMB  uint64

	chunksRemaining atomic.Int64 // per-run chunk budget; -1 = unlimited
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

// IndexProgressFunc is called periodically during IndexProject (done/total files).
type IndexProgressFunc func(done, total, filesIndexed, chunksCreated, errors int)

// IndexerOpts groups optional tuning parameters for NewIndexer.
type IndexerOpts struct {
	Workers             int
	EmbedBatchSize      int
	MaxFileSize         int
	MaxChunksPerFile    int
	MaxChunksPerProject int
	MaxFilesPerProject  int
	Verbose             bool
	GitMode             bool
	GitSince            string
	Logger              *slog.Logger
	OnProgress          IndexProgressFunc
}

// NewIndexer wires an Indexer. dims is the embedding dimension of the model;
// opts carries optional tuning parameters (zero-valued fields get defaults).
func NewIndexer(db store.IndexStore, emb embed.Embedder, dims int, opts IndexerOpts) *Indexer {
	if opts.Workers < 1 {
		opts.Workers = defaultIndexWorkers
	}
	if opts.EmbedBatchSize < 1 {
		opts.EmbedBatchSize = 8
	}
	if opts.MaxFileSize < 1 {
		opts.MaxFileSize = 1024 * 1024
	}
	if opts.MaxChunksPerFile < 1 {
		opts.MaxChunksPerFile = 32
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	idx := &Indexer{db: db, embedder: emb, dims: dims, workers: opts.Workers, embedBatchSize: opts.EmbedBatchSize, maxFileSize: opts.MaxFileSize, maxChunksPerFile: opts.MaxChunksPerFile, maxChunksPerProject: opts.MaxChunksPerProject, maxFilesPerProject: opts.MaxFilesPerProject, maxChunkChars: 4000, verbose: opts.Verbose, gitMode: opts.GitMode, gitSince: opts.GitSince, log: opts.Logger, onProgress: opts.OnProgress}
	if opts.MaxChunksPerProject > 0 {
		idx.chunksRemaining.Store(int64(opts.MaxChunksPerProject))
	} else {
		idx.chunksRemaining.Store(-1)
	}
	return idx
}

// SetKeywordOnly switches the indexer to keyword-only mode: chunks are stored as
// text (embedding NULL) and no embedding provider is called. Returns the indexer
// for chaining.
func (idx *Indexer) SetKeywordOnly(v bool) *Indexer {
	idx.keywordOnly = v
	return idx
}

// SetSecretScan configures the indexer for gitleaks-based secret scanning. When
// enabled, each file is scanned before chunking; files with detected secrets are
// stored text-only (no embedding) when blockEmbedding is true. The detector is
// created once and reused across all files of the project.
func (idx *Indexer) SetSecretScan(root string, enabled, blockEmbedding bool) *Indexer {
	idx.secretScanEnabled = enabled
	idx.secretBlockEmbedding = blockEmbedding
	if enabled {
		d, err := secrets.NewDetector(root)
		if err != nil {
			idx.log.Warn("secret detector init (disabled)", "root", root, "err", err)
			idx.secretScanEnabled = false
		} else {
			idx.secretDetector = d
		}
	}
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
// git history, and marks the project ready. When the project is a git repo and a
// previous index stored a commit SHA, only files changed since that commit are
// processed (git-diff incremental layer).
func (idx *Indexer) IndexProject(ctx context.Context, projectID int, projectPath, model string, maxFiles int) (*IndexStats, error) {
	ctx, span := observ.StartSpan(ctx, "indexing.Indexer.IndexProject")
	defer span.End()

	stats := &IndexStats{}

	if idx.modulePath == "" {
		idx.modulePath = ReadModulePath(projectPath)
	}

	files, err := idx.resolveIndexFiles(ctx, projectID, projectPath, maxFiles)
	if err != nil {
		return nil, err
	}
	stats.FilesScanned = len(files)

	var (
		mu        sync.Mutex
		processed int
		g         errgroup.Group
		manifest  = map[string]string{}
	)
	g.SetLimit(idx.workers)

	for _, path := range files {
		if ctx.Err() != nil {
			break
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
			filesIdx, errs := stats.FilesIndexed, stats.Errors
			total := len(files)
			mu.Unlock()

			idx.progress(done, total, rel, chunks, filesIdx, errs)
			return nil
		})
	}
	_ = g.Wait()

	if err := ctx.Err(); err != nil {
		return stats, err
	}

	idx.finalizeProject(ctx, projectID, projectPath, model, stats, manifest)
	return stats, nil
}

// resolveIndexFiles returns the files to index: git-diff incremental when a
// previous commit is stored, otherwise a full walk of projectPath.
func (idx *Indexer) resolveIndexFiles(ctx context.Context, projectID int, projectPath string, maxFiles int) ([]string, error) {
	changedFiles, diffErr := idx.gitDiffChanged(ctx, projectID, projectPath)
	if diffErr != nil {
		idx.logf("[warn] git diff incremental failed, falling back to full walk: %s", truncateErr(diffErr, 200))
	}
	if len(changedFiles) > 0 {
		files := make([]string, 0, len(changedFiles))
		for _, rel := range changedFiles {
			files = append(files, filepath.Join(projectPath, rel))
		}
		return files, nil
	}
	return ScanFiles(projectPath, effectiveMaxFiles(maxFiles, idx.maxFilesPerProject))
}

// effectiveMaxFiles combines a per-run limit with a server/project cap (0 = unlimited).
func effectiveMaxFiles(requestMax, projectCap int) int {
	switch {
	case requestMax > 0 && projectCap > 0:
		return min(requestMax, projectCap)
	case requestMax > 0:
		return requestMax
	default:
		return projectCap
	}
}

// takeChunkBudget reserves up to want chunks from the per-project budget.
// Returns 0 when the cap is exhausted. Unlimited budgets return want unchanged.
func (idx *Indexer) takeChunkBudget(want int) int {
	if want <= 0 {
		return 0
	}
	for {
		rem := idx.chunksRemaining.Load()
		if rem < 0 {
			return want
		}
		if rem == 0 {
			return 0
		}
		take := want
		if int64(take) > rem {
			take = int(rem)
		}
		if idx.chunksRemaining.CompareAndSwap(rem, rem-int64(take)) {
			return take
		}
	}
}

// finalizeProject runs the post-index steps: worktree manifest, git history,
// commit SHA storage, and status update. All steps are best-effort.
func (idx *Indexer) finalizeProject(ctx context.Context, projectID int, projectPath, model string, stats *IndexStats, manifest map[string]string) {
	if idx.worktree != "" {
		idx.recordWorktree(ctx, projectID, manifest)
	}

	if idx.gitMode {
		if err := idx.indexGitHistory(ctx, projectID, projectPath, model); err != nil {
			stats.Errors++
			idx.logf("[err] git history: %s", truncateErr(err, 200))
		}
	}

	if commitSHA := idx.gitHeadCommit(ctx, projectPath); commitSHA != "" {
		if err := idx.db.UpdateProjectCommit(ctx, projectID, commitSHA); err != nil {
			idx.logf("[warn] store indexed commit: %s", truncateErr(err, 200))
		}
	}

	if err := idx.db.UpdateProjectStatus(ctx, projectID, "ready"); err != nil {
		idx.log.Warn("update project status", "project", projectID, "err", err)
	}
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
		// Per-project chunk cap: if the total would exceed the limit, log a
		// warning and skip counting (storeChunks already enforced the budget).
		if idx.maxChunksPerProject > 0 && stats.ChunksCreated+r.created > idx.maxChunksPerProject {
			idx.logf("[warn] project chunk limit (%d) reached, not counting %d chunk(s) from %s",
				idx.maxChunksPerProject, r.created, rel)
		} else {
			stats.ChunksCreated += r.created
		}
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
		if Eligible(rel) {
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
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, nil, err
	}
	content, err := io.ReadAll(io.LimitReader(f, int64(idx.maxFileSize)))
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
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return false, nil, err
	}
	content, err := io.ReadAll(io.LimitReader(f, int64(idx.maxFileSize)))
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
	if len(content) > idx.maxFileSize {
		content = content[:idx.maxFileSize]
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

	chunks := chunker.ChunkFile(rel, content, idx.maxChunkChars)
	if len(chunks) == 0 {
		return 0, 0, outcomeSkippedEmpty, "", nil
	}
	if len(chunks) > idx.maxChunksPerFile {
		chunks = chunks[:idx.maxChunksPerFile]
	}

	// Extract import dependencies for graph edges (all supported languages).
	deps := si.Analyze(rel, content, idx.modulePath)
	// Replace edges every index pass so stale imports are removed.
	if err := idx.db.InsertFileDependencies(ctx, projectID, rel, deps); err != nil {
		idx.log.Warn("failed to record dependencies", "file", rel, "error", err)
	}

	if err := idx.db.DeleteChunksForFile(ctx, projectID, fileID, idx.dims); err != nil {
		return 0, 0, outcomeSkippedEmpty, "", err
	}

	var syms []sym.Symbol
	if !idx.keywordOnly && !idx.noSymbols {
		syms = sym.Symbols(rel, content)
	}

	// Secret scan: run before embedding. When secrets are detected and
	// blockEmbedding is on, the file is stored text-only (no embedding).
	hasSecrets := false
	if idx.secretScanEnabled && idx.secretDetector != nil {
		findings := idx.secretDetector.Scan(rel, content)
		if len(findings) > 0 {
			hasSecrets = true
			idx.logf("[secrets] %s: %d finding(s) — stored text-only", rel, len(findings))
		}
	}

	created, softErrs, err = idx.storeChunks(ctx, chunkStoreParams{
		projectID:  projectID,
		fileID:     fileID,
		rel:        rel,
		model:      model,
		chunks:     chunks,
		syms:       syms,
		hasSecrets: hasSecrets,
	})
	if err != nil {
		return 0, 0, outcomeSkippedEmpty, "", err
	}
	return created, softErrs, outcomeIndexed, hash, nil
}

// chunkStoreParams groups the arguments to storeChunks that describe the file
// being indexed and its chunking context.
type chunkStoreParams struct {
	projectID  int
	fileID     int
	rel        string
	model      string
	chunks     []chunker.Chunk
	syms       []sym.Symbol
	hasSecrets bool
}

// storeChunks turns a unit's chunks into stored rows, honoring keyword-only mode,
// privacy routing and secret-scan tagging. It stores the chunks text-only in
// keyword-only mode, when a sensitive file cannot be embedded by a local provider,
// or when secrets were detected and SECRET_BLOCK_EMBEDDING is true; otherwise it
// embeds them (forcing a local provider for sensitive files).
func (idx *Indexer) storeChunks(ctx context.Context, p chunkStoreParams) (created, softErrs int, hardErr error) {
	budget := idx.takeChunkBudget(len(p.chunks))
	if budget == 0 {
		idx.logf("[warn] project chunk limit reached, skipping chunks for %s", p.rel)
		return 0, 0, nil
	}
	if budget < len(p.chunks) {
		idx.logf("[warn] project chunk limit: storing %d of %d chunk(s) for %s", budget, len(p.chunks), p.rel)
		p.chunks = p.chunks[:budget]
	}

	// Keyword-only mode: no embedding provider at all — store the chunks as text
	// so they stay searchable by keyword (FTS/ILIKE) without any model.
	if idx.keywordOnly {
		if err := idx.db.InsertChunksTextOnly(ctx, p.projectID, p.fileID, p.chunks, idx.dims); err != nil {
			return 0, 0, err
		}
		return len(p.chunks), 0, nil
	}

	// Privacy routing + secret scan: a sensitive file or one with detected
	// secrets must never be embedded by a cloud provider. If a local provider
	// can serve the model, embed locally; if not, store the content as text-only
	// (embedding NULL) so it stays findable via keyword search without ever
	// leaving the machine.
	embedCtx := ctx
	isSensitive := privacy.IsSensitive(p.rel) || (p.hasSecrets && idx.secretBlockEmbedding)
	if isSensitive {
		localCtx := embed.WithForceLocal(ctx, true)
		if _, err := idx.embedder.ModelInfo(localCtx, p.model); err != nil {
			if err := idx.db.InsertChunksTextOnly(ctx, p.projectID, p.fileID, p.chunks, idx.dims); err != nil {
				return 0, 0, err
			}
			idx.logf("  [local-text] %s (sensitive; stored text-only, no cloud embedding)", p.rel)
			return len(p.chunks), 0, nil
		}
		embedCtx = localCtx
	}

	created, softErrs = idx.embedAndInsert(embedCtx, p.projectID, p.fileID, p.chunks, p.model, p.rel, p.syms)
	return created, softErrs, nil
}

// buildChunkInputs builds input text strings for each chunk, prefixing symbols
// whose line range overlaps each chunk (capped at 20 symbols).
func buildChunkInputs(chunks []chunker.Chunk, syms []sym.Symbol) []string {
	inputs := make([]string, len(chunks))
	for j, c := range chunks {
		var chunkSyms []string
		for _, s := range syms {
			if s.StartLine <= c.EndLine && s.EndLine >= c.StartLine {
				chunkSyms = append(chunkSyms, s.Kind+" "+s.Name)
			}
		}
		if len(chunkSyms) > 20 {
			chunkSyms = append(chunkSyms[:20], "...")
		}
		if len(chunkSyms) > 0 {
			inputs[j] = "Symbols: " + strings.Join(chunkSyms, ", ") + "\n" + c.Content
		} else {
			inputs[j] = c.Content
		}
	}
	return inputs
}

// embedAndInsert embeds chunks in sub-batches and inserts each successful batch.
// Checks the embedding cache before calling the embedder; only uncached entries
// trigger API calls (partial-batch aware).
func (idx *Indexer) embedAndInsert(ctx context.Context, projectID, fileID int, chunks []chunker.Chunk, model, rel string, syms []sym.Symbol) (created, softErrs int) {
	for start := 0; start < len(chunks); start += idx.embedBatchSize {
		end := min(start+idx.embedBatchSize, len(chunks))
		sub := chunks[start:end]
		inputs := buildChunkInputs(sub, syms)
		hashes := computeHashes(inputs)

		cached, cacheErr := idx.db.LookupEmbeddingCache(ctx, hashes, model, idx.dims)
		if cacheErr != nil {
			idx.logf("[warn] cache lookup %s batch %d-%d: %s", rel, start, end, truncateErr(cacheErr, 200))
		}

		res := resolveBatchEmbeddings(inputs, hashes, cached)

		if res.uncachedCount == 0 {
			idx.logf("  [cache-hit] %s batch %d-%d: %d/%d cached", rel, start, end, res.hitCount, res.totalCount)
		} else {
			soft, ok := idx.embedUncachedBatch(ctx, model, rel, start, end, &res)
			if soft > 0 {
				softErrs += soft
			}
			if !ok {
				continue
			}
		}

		if err := idx.db.InsertChunks(ctx, projectID, fileID, sub, res.embeddings, idx.dims); err != nil {
			softErrs++
			idx.logf("[err] insert %s batch %d-%d: %s", rel, start, end, truncateErr(err, 200))
			continue
		}
		created += len(sub)
	}
	return created, softErrs
}

// batchResult holds the partition state for one embedding batch after cache
// resolution: which entries are cached, which need embedding, and the merged
// final embeddings slice (cache hits pre-populated, uncached slots filled later).
type batchResult struct {
	embeddings     [][]float32
	uncachedIdx    []int
	uncachedInputs []string
	uncachedHashes []string
	uncachedCount  int
	hitCount       int
	totalCount     int
}

// computeHashes returns hex-encoded SHA-256 hashes for each input string.
func computeHashes(inputs []string) []string {
	hashes := make([]string, len(inputs))
	for i, input := range inputs {
		h := sha256.Sum256([]byte(input))
		hashes[i] = fmt.Sprintf("%x", h)
	}
	return hashes
}

// resolveBatchEmbeddings partitions a batch into cached and uncached entries.
// On cache-hit, embeddings are pre-filled; uncached slots are left nil to be
// filled by the embedder. When cached is nil (lookup error), all entries are
// treated as uncached.
func resolveBatchEmbeddings(inputs, hashes []string, cached map[string][]float32) batchResult {
	total := len(inputs)
	res := batchResult{
		embeddings:     make([][]float32, total),
		uncachedIdx:    make([]int, 0, total),
		uncachedInputs: make([]string, 0, total),
		uncachedHashes: make([]string, 0, total),
		totalCount:     total,
	}

	for i, h := range hashes {
		if emb, ok := cached[h]; ok {
			res.embeddings[i] = emb
			res.hitCount++
		} else {
			res.uncachedIdx = append(res.uncachedIdx, i)
			res.uncachedInputs = append(res.uncachedInputs, inputs[i])
			res.uncachedHashes = append(res.uncachedHashes, h)
		}
	}
	res.uncachedCount = len(res.uncachedIdx)
	return res
}

// embedUncachedBatch calls the embedding API for uncached entries, stores new
// embeddings in the cache, and merges them into the batch result. Returns (softErrs, ok).
func (idx *Indexer) embedUncachedBatch(ctx context.Context, model, rel string, start, end int, res *batchResult) (int, bool) {
	embeddings, err := idx.embedWithRetry(ctx, model, res.uncachedInputs)
	if err != nil {
		idx.logf("[err] embed %s batch %d-%d: %s", rel, start, end, truncateErr(err, 200))
		return 1, false
	}

	if err := idx.db.InsertEmbeddingCache(ctx, res.uncachedHashes, model, embeddings, idx.dims); err != nil {
		idx.logf("[warn] cache insert %s: %s", rel, truncateErr(err, 200))
	}

	for i, origIdx := range res.uncachedIdx {
		res.embeddings[origIdx] = embeddings[i]
	}

	if res.hitCount > 0 {
		idx.logf("  [cache-partial] %s batch %d-%d: %d/%d cached, %d new",
			rel, start, end, res.hitCount, res.totalCount, res.uncachedCount)
	}
	return 0, true
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
	maxJitter := int64(backoff / 2)
	var jitter int64
	if maxJitter > 0 {
		if n, err := rand.Int(rand.Reader, big.NewInt(maxJitter)); err == nil {
			jitter = n.Int64()
		}
	}
	delay := backoff + time.Duration(jitter)
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// runGitLog executes git log -p with an optional --since window and returns the
// raw output. The projectPath is validated with safeGitDir first. ctx cancels a
// long-running log when the job or server shuts down.
func runGitLog(ctx context.Context, projectPath, gitSince string) ([]byte, error) {
	if !safeGitDir(projectPath) {
		return nil, fmt.Errorf("unsafe git project path: %q", projectPath)
	}
	args := []string{"-C", projectPath, "log", "-p"}
	if gitSince != "" {
		if !safeGitRef(gitSince) {
			return nil, fmt.Errorf("unsafe git since value: %q", gitSince)
		}
		args = append(args, "--since="+gitSince)
	}
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = append([]string{"git"}, args...)
	cmd.Env = gitenv.Clean(cmd.Environ())
	return cmd.Output()
}

func (idx *Indexer) indexGitHistory(ctx context.Context, projectID int, projectPath, model string) error {
	out, err := runGitLog(ctx, projectPath, idx.gitSince)
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
		if idx.indexCommit(ctx, projectID, model, commit) {
			if idx.verbose && i%10 == 0 {
				idx.log.Info("git indexing progress", "commits_processed", i+1)
			}
		}
	}
	return nil
}

// indexCommit embeds and stores a single `git log -p` commit block as one chunk,
// returning true only when the chunk was stored. Every step is best-effort: any
// error skips this commit without failing the history pass.
//
// Uses the embedding cache to avoid re-embedding identical commits across
// worktrees and re-index cycles (added in feat/embedding-cache Phase 1).
func (idx *Indexer) indexCommit(ctx context.Context, projectID int, model string, commit []byte) bool {
	if len(commit) > idx.maxChunkChars {
		commit = commit[:idx.maxChunkChars]
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

	embedding, ok := idx.embedSingleWithCache(ctx, model, string(commit))
	if !ok {
		return false
	}
	chunk := chunker.Chunk{Content: string(commit), StartLine: 1, EndLine: 1}
	return idx.db.InsertChunks(ctx, projectID, fileID, []chunker.Chunk{chunk}, [][]float32{embedding}, idx.dims) == nil
}

// embedSingleWithCache returns an embedding for text, using the cache when
// available. The hash key is SHA-256(text) — git commit messages are plain text
// without symbol enrichment, so the key matches the cache schema exactly.
// Returns (nil, false) on any error (best-effort; the caller skips the commit).
func (idx *Indexer) embedSingleWithCache(ctx context.Context, model, text string) ([]float32, bool) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))

	cached, err := idx.db.LookupEmbeddingCache(ctx, []string{hash}, model, idx.dims)
	if err == nil {
		if emb, ok := cached[hash]; ok {
			return emb, true
		}
	}
	// Cache miss or lookup error — compute and store.
	embedding, err := idx.embedder.EmbedSingle(ctx, model, text)
	if err != nil {
		return nil, false
	}
	// Best-effort cache insert (non-fatal).
	if err := idx.db.InsertEmbeddingCache(ctx, []string{hash}, model, [][]float32{embedding}, idx.dims); err != nil {
		idx.logf("[warn] cache insert git commit: %s", truncateErr(err, 200))
	}
	return embedding, true
}

// gitDiffChanged returns the list of files changed since the last stored
// commit, or nil on any failure. The returned paths are relative to the repo
// root. If no commit is stored (first index), it returns nil to trigger a full
// walk. The method supports a force flag via the context (not yet wired).
func (idx *Indexer) gitDiffChanged(ctx context.Context, projectID int, projectPath string) ([]string, error) {
	// Only applies to git projects.
	if !isGitDir(projectPath) {
		return nil, nil
	}

	storedSHA, err := idx.db.GetProjectCommit(ctx, projectID)
	if err != nil || storedSHA == "" {
		return nil, nil // first index or store error — full walk
	}

	// Run `git diff --name-only <storedSHA>..HEAD`.
	return gitDiffNames(ctx, projectPath, storedSHA)
}

// gitHeadCommit returns the current HEAD commit SHA, or "" on failure.
func (idx *Indexer) gitHeadCommit(ctx context.Context, projectPath string) string {
	if !isGitDir(projectPath) {
		return ""
	}
	sha, err := gitRun(ctx, projectPath, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return sha
}

// isGitDir checks whether projectPath is inside a git repo.
func isGitDir(projectPath string) bool {
	_, err := gitRun(context.Background(), projectPath, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

// gitDiffNames returns the list of files (relative to repo root) that differ
// between baseRef and HEAD.
func gitDiffNames(ctx context.Context, projectPath, baseRef string) ([]string, error) {
	out, err := gitRun(ctx, projectPath, "diff", "--name-only", baseRef+"..HEAD")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(strings.TrimSpace(out), "\n"), nil
}

// safeGitDir checks that a project path does not contain path-traversal
// sequences or start with a dash (which would be parsed as a git flag).
func safeGitDir(dir string) bool {
	return !strings.Contains(dir, "..") && !strings.HasPrefix(dir, "-") && !strings.HasPrefix(dir, "~")
}

// safeGitRef checks that a ref value contains only safe characters and does not
// start with a dash.
func safeGitRef(ref string) bool {
	if ref == "" || ref[0] == '-' {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '/', r == '-', r == ':', r == '@':
		default:
			return false
		}
	}
	return true
}

// gitRun executes a git subcommand in projectPath with hermetic config and
// returns trimmed stdout, or an error if the command fails.
func gitRun(ctx context.Context, projectPath string, args ...string) (string, error) {
	if !safeGitDir(projectPath) {
		return "", fmt.Errorf("unsafe git project path: %q", projectPath)
	}
	fullArgs := append([]string{"git", "-C", projectPath}, args...)
	cmd := exec.CommandContext(ctx, "git")
	cmd.Args = fullArgs
	cmd.Env = gitenv.Clean(cmd.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (idx *Indexer) logf(format string, args ...any) {
	if idx.verbose {
		idx.log.Info(fmt.Sprintf(format, args...))
	}
}

func (idx *Indexer) progress(done, total int, rel string, chunks, filesIndexed, errors int) {
	if idx.onProgress != nil && (done%logEvery == 0 || done == total) {
		idx.onProgress(done, total, filesIndexed, chunks, errors)
	}
	if idx.verbose {
		heapMB, sysMB := idx.memStats()
		idx.log.Info("file indexed",
			"progress", fmt.Sprintf("%d/%d", done, total),
			"path", rel, "chunks", chunks,
			"heap_mb", heapMB, "sys_mb", sysMB)
	} else if done%logEvery == 0 || done == total {
		idx.log.Info("indexing progress",
			"progress", fmt.Sprintf("%d/%d", done, total),
			"chunks", chunks)
	}
}

// memStats returns the live heap and system allocation sizes in MB,
// throttled to one runtime.ReadMemStats call per 10s. ReadMemStats triggers a
// stop-the-world; calling it on every progress tick (every file) measurably
// slows indexing with many workers, so the throttle sacrifices sub-second
// resolution for throughput.
func (idx *Indexer) memStats() (uint64, uint64) {
	now := time.Now()
	idx.memMu.Lock()
	if now.Sub(idx.memAt) < 10*time.Second {
		h, s := idx.lastHeapMB, idx.lastSysMB
		idx.memMu.Unlock()
		return h, s
	}
	idx.memAt = now
	idx.memMu.Unlock()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	h, s := m.Alloc/1024/1024, m.Sys/1024/1024
	idx.memMu.Lock()
	idx.lastHeapMB, idx.lastSysMB = h, s
	idx.memMu.Unlock()
	return h, s
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
