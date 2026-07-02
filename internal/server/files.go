package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

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

// handleFilesBatch indexes uploaded file contents (the server chunks and embeds,
// so credentials stay on the server) and removes any files in the delete list.
func (s *Server) handleFilesBatch(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
		Delete []string `json:"delete"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	ctx := r.Context()
	info, err := s.emb.ModelInfo(ctx, proj.Model)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "model unavailable: "+err.Error())
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
		created, err := idx.IndexContent(ctx, proj.ID, f.Path, proj.Model, []byte(f.Content))
		if err != nil {
			failed++
			s.log.Error("index pushed file", "project", proj.Name, "path", f.Path, "err", err)
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
