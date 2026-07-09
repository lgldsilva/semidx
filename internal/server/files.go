package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/lgldsilva/semidx/internal/chunker"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/store"
)

// Limits enforced on pre-embedded chunks pushed by clients.
const (
	maxPreChunksPerFile = 32
	maxPreChunkChars    = 4000
)

// embeddedChunk is a pre-computed chunk sent by a client via files/batch.
type embeddedChunk struct {
	StartLine int       `json:"start_line"`
	EndLine   int       `json:"end_line"`
	Content   string    `json:"content"`
	Embedding []float32 `json:"embedding"`
}

func validatePreEmbeddedChunks(path string, fileChunks []embeddedChunk, dims int, model string) error {
	if privacy.IsSensitive(path) {
		return fmt.Errorf("path %q is classified as sensitive; pre-embedded uploads are not allowed", path)
	}
	for i, ch := range fileChunks {
		if len(ch.Embedding) != dims {
			return fmt.Errorf("embedding dimension mismatch (chunk %d): got %d dims, expected %d for model %q",
				i, len(ch.Embedding), dims, model)
		}
	}
	if len(fileChunks) > maxPreChunksPerFile {
		return fmt.Errorf("too many chunks: %d (max %d)", len(fileChunks), maxPreChunksPerFile)
	}
	for i, ch := range fileChunks {
		if len(ch.Content) > maxPreChunkChars {
			return fmt.Errorf("chunk %d too large: %d chars (max %d)", i, len(ch.Content), maxPreChunkChars)
		}
	}
	return nil
}

// batchFileInput is one file in a files/batch push: pre-embedded chunks, raw
// content, or (invalid) neither.
type batchFileInput struct {
	Path    string          `json:"path"`
	Content string          `json:"content,omitempty"`
	Chunks  []embeddedChunk `json:"chunks,omitempty"`
}

// errNoContentOrChunks marks a pushed file that carried neither raw content nor
// pre-computed chunks — nothing to index.
var errNoContentOrChunks = errors.New("file has no content or chunks")

// handleFilesDiff reports which of the client's files are new/changed ("stale",
// to upload) and which are indexed but no longer present ("deleted"). Read-only.
func (s *Server) handleFilesDiff(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Files map[string]string `json:"files"` // path -> content hash
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	existing, err := s.store.ListFileHashes(r.Context(), proj.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not read project files")
		return
	}

	stale := make([]string, 0)
	for path, hash := range body.Files {
		if err := validateRelativePath(path); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid path "+path+": "+err.Error())
			return
		}
		if existing[path] != hash { // new or changed
			stale = append(stale, path)
		}
	}
	deleted := make([]string, 0)
	for path := range existing {
		if _, present := body.Files[path]; !present {
			deleted = append(deleted, path)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stale": stale, "deleted": deleted})
}

// batchRequestBody is the JSON shape accepted by the files/batch endpoint.
type batchRequestBody struct {
	Files  []batchFileInput `json:"files"`
	Delete []string         `json:"delete"`
}

// handleFilesBatch indexes uploaded file contents and removes any files in the
// delete list. By default it returns 202 Accepted and processes the batch
// asynchronously via a background job. Pass ?sync=true to get the old
// synchronous behaviour (200 OK with inline results).
func (s *Server) handleFilesBatch(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var body batchRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := validateBatchBody(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if r.URL.Query().Get("sync") == "true" {
		s.handleFilesBatchSync(w, r, proj, &body)
		return
	}

	// Async path: enqueue a batch job.
	if proj.SourceType != "push" {
		writeJSONError(w, http.StatusBadRequest, "async batch is only supported for push projects")
		return
	}
	payload, err := json.Marshal(body)
	if err != nil {
		s.log.Error("marshal batch payload", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not serialize payload")
		return
	}
	jobID, err := s.store.EnqueueBatchJob(r.Context(), proj.ID, string(payload))
	if err != nil {
		s.log.Error("enqueue batch job", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not enqueue batch job")
		return
	}
	s.jobsQueued.Inc()
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "status": "queued"})
}

// handleFilesBatchSync runs the batch synchronously and returns 200 with
// indexing counts — the original behaviour preserved behind ?sync=true.
func (s *Server) handleFilesBatchSync(w http.ResponseWriter, r *http.Request, proj *store.Project, body *batchRequestBody) {
	ctx := r.Context()
	info, err := s.emb.ModelInfo(ctx, proj.Model)
	if err != nil {
		s.log.Warn("model info lookup failed", "project", proj.Name, "model", proj.Model, "err", err)
		writeJSONError(w, http.StatusBadGateway, "model unavailable")
		return
	}
	if err := s.store.EnsureChunksTable(ctx, info.Dims); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not prepare storage")
		return
	}
	indexed, chunks, deleted, errors := s.processBatchFiles(ctx, proj, body.Files, body.Delete, info.Dims)
	writeJSON(w, http.StatusOK, map[string]any{
		"indexed": indexed, "chunks": chunks, "deleted": deleted, "errors": errors,
	})
}

func validateBatchBody(body *batchRequestBody) error {
	for _, f := range body.Files {
		if err := validateRelativePath(f.Path); err != nil {
			return fmt.Errorf("invalid path %s: %w", f.Path, err)
		}
	}
	for _, p := range body.Delete {
		if err := validateRelativePath(p); err != nil {
			return fmt.Errorf("invalid delete path %s: %w", p, err)
		}
	}
	return nil
}

// processBatchFiles indexes pushed files and removes deleted ones, sharing the
// same core logic between the synchronous API path and the background batch
// worker. Expects the chunks table and model info to already be set up.
func (s *Server) processBatchFiles(ctx context.Context, proj *store.Project, files []batchFileInput, del []string, dims int) (indexed, chunks, deleted, errors int) {
	deleted = len(del)
	for _, p := range del {
		if err := s.store.DeleteFileByPath(ctx, proj.ID, p); err != nil {
			s.log.Error("delete file", "project", proj.Name, "path", p, "err", err)
		}
	}

	idx := indexing.NewIndexer(s.store, s.emb, dims, indexing.IndexerOpts{})
	for _, f := range files {
		created, hErr := s.indexBatchFile(ctx, proj, idx, f, dims)
		if hErr != nil {
			errors++
			s.log.Error("index pushed file", "project", proj.Name, "path", f.Path, "err", hErr)
			continue
		}
		indexed++
		chunks += created
	}
	if err := s.store.UpdateProjectStatus(ctx, proj.ID, "ready"); err != nil {
		s.log.Warn("update project status", "project", proj.ID, "err", err)
	}
	return
}

// indexBatchFile stores one pushed file, dispatching on its shape: pre-embedded
// chunks are validated and stored directly; raw content goes through the
// server-side chunk+embed pipeline; a file with neither is an error. It returns
// the number of chunks created.
func (s *Server) indexBatchFile(ctx context.Context, proj *store.Project, idx *indexing.Indexer, f batchFileInput, dims int) (int, error) {
	switch {
	case len(f.Chunks) > 0:
		// Pre-embedded: the client already chunked and embedded.
		return s.indexPreEmbedded(ctx, proj, f.Path, f.Content, f.Chunks, dims)
	case f.Content != "":
		// Raw content: server handles chunking and embedding.
		return idx.IndexContent(ctx, proj.ID, f.Path, proj.Model, []byte(f.Content))
	default:
		return 0, errNoContentOrChunks
	}
}

// indexPreEmbedded validates pre-computed chunk embeddings and stores them
// directly. The raw content is still hashed and recorded for file dedup; only
// chunking and embedding are skipped.
func (s *Server) indexPreEmbedded(ctx context.Context, proj *store.Project, path, rawContent string, fileChunks []embeddedChunk, dims int) (int, error) {
	if err := validatePreEmbeddedChunks(path, fileChunks, dims, proj.Model); err != nil {
		return 0, err
	}

	// Content-addressed dedup: same hash → file is unchanged, skip.
	contentBytes := []byte(rawContent)
	contentHash := fmt.Sprintf("%x", sha256.Sum256(contentBytes))

	upToDate, err := s.store.FileUpToDate(ctx, proj.ID, path, contentHash, dims)
	if err != nil {
		return 0, fmt.Errorf("check file up-to-date: %w", err)
	}
	if upToDate {
		return 0, nil
	}

	fileID, err := s.store.UpsertFile(ctx, proj.ID, path, contentHash, len(contentBytes))
	if err != nil {
		return 0, fmt.Errorf("upsert file: %w", err)
	}

	// Delete old chunks for this file before inserting the new set.
	if err := s.store.DeleteChunksForFile(ctx, proj.ID, fileID, dims); err != nil {
		return 0, fmt.Errorf("delete old chunks: %w", err)
	}

	// Convert to the store's chunk types and insert.
	storeChunks := make([]chunker.Chunk, len(fileChunks))
	embeddings := make([][]float32, len(fileChunks))
	for i, ch := range fileChunks {
		storeChunks[i] = chunker.Chunk{
			Content:   ch.Content,
			StartLine: ch.StartLine,
			EndLine:   ch.EndLine,
		}
		embeddings[i] = ch.Embedding
	}

	if err := s.store.InsertChunks(ctx, proj.ID, fileID, storeChunks, embeddings, dims); err != nil {
		return 0, fmt.Errorf("insert chunks: %w", err)
	}

	// Keep embedding cache warm on pre-embedded pushes too (REQ-EMB-07), so
	// later reindexes and repeated uploads can reuse content-addressed vectors.
	hashes := make([]string, len(fileChunks))
	for i, ch := range fileChunks {
		h := sha256.Sum256([]byte(ch.Content))
		hashes[i] = fmt.Sprintf("%x", h)
	}
	if err := s.store.InsertEmbeddingCache(ctx, hashes, proj.Model, embeddings, dims); err != nil {
		s.log.Warn("cache insert pre-embedded chunks", "project", proj.Name, "path", path, "err", err)
	}

	return len(fileChunks), nil
}

// loadProject resolves the {project} path value or writes 404/500 and returns false.
func (s *Server) loadProject(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	proj, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return nil, false
	}
	return proj, true
}
