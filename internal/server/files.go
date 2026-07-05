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

// handleFilesBatch indexes uploaded file contents and removes any files in the
// delete list. Files may carry pre-computed chunks with embeddings (the client
// did the work) or raw content (the server chunks and embeds). Mixed batches
// are supported: each file follows its own path.
func (s *Server) handleFilesBatch(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Files  []batchFileInput `json:"files"`
		Delete []string         `json:"delete"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

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

	for _, p := range body.Delete {
		if err := s.store.DeleteFileByPath(ctx, proj.ID, p); err != nil {
			s.log.Error("delete file", "project", proj.Name, "path", p, "err", err)
		}
	}

	idx := indexing.NewIndexer(s.store, s.emb, info.Dims, 0, false, false, "")
	indexed, chunks, failed := 0, 0, 0
	for _, f := range body.Files {
		created, hErr := s.indexBatchFile(ctx, proj, idx, f, info.Dims)
		if hErr != nil {
			failed++
			s.log.Error("index pushed file", "project", proj.Name, "path", f.Path, "err", hErr)
			continue
		}
		indexed++
		chunks += created
	}
	_ = s.store.UpdateProjectStatus(ctx, proj.ID, "ready")

	writeJSON(w, http.StatusOK, map[string]any{
		"indexed": indexed, "chunks": chunks, "deleted": len(body.Delete), "errors": failed,
	})
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
	// P0: validate dimensions match the project's model.
	for i, ch := range fileChunks {
		if len(ch.Embedding) != dims {
			return 0, fmt.Errorf("embedding dimension mismatch (chunk %d): got %d dims, expected %d for model %q",
				i, len(ch.Embedding), dims, proj.Model)
		}
	}

	// P0: enforce chunk limits to prevent rogue clients from flooding the DB.
	if len(fileChunks) > maxPreChunksPerFile {
		return 0, fmt.Errorf("too many chunks: %d (max %d)", len(fileChunks), maxPreChunksPerFile)
	}
	for i, ch := range fileChunks {
		if len(ch.Content) > maxPreChunkChars {
			return 0, fmt.Errorf("chunk %d too large: %d chars (max %d)", i, len(ch.Content), maxPreChunkChars)
		}
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
