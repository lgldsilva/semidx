package webadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

const (
	adminIngestMaxFiles     = 50
	adminIngestMaxFileBytes = 512 * 1024 // 512 KiB per file (browser small-batch)
)

type ingestFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type ingestBody struct {
	Files  []ingestFile `json:"files"`
	Delete []string     `json:"delete"`
}

// apiProjectFilesBatch indexes raw file contents uploaded from the admin SPA
// (sync). Intended for small batches; large corpora should use CLI push.
func (a *Admin) apiProjectFilesBatch(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	proj, err := a.store.GetProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	if a.emb == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "no embedder configured on server")
		return
	}

	var body ingestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Files) == 0 && len(body.Delete) == 0 {
		writeJSONErr(w, http.StatusBadRequest, "files or delete required")
		return
	}
	if len(body.Files) > adminIngestMaxFiles {
		writeJSONErr(w, http.StatusBadRequest, "too many files (max 50 per batch)")
		return
	}

	ctx := r.Context()
	model := proj.Model
	if model == "" {
		model = "bge-m3"
	}
	info, err := a.emb.ModelInfo(ctx, model)
	if err != nil {
		a.log.Warn("model info for ingest", "err", err)
		writeJSONErr(w, http.StatusBadGateway, "embedding model unavailable — configure a provider or use keyword mode on CLI")
		return
	}
	dims := info.Dims
	if dims <= 0 {
		dims = 1024
	}
	if err := a.store.EnsureChunksTable(ctx, dims); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "could not prepare storage")
		return
	}

	deleted := 0
	for _, p := range body.Delete {
		p = cleanRelPath(p)
		if p == "" {
			continue
		}
		if err := a.store.DeleteFileByPath(ctx, proj.ID, p); err != nil {
			a.log.Error("delete file on ingest", "path", p, "err", err)
			continue
		}
		deleted++
	}

	idx := indexing.NewIndexer(a.store, a.emb, dims, indexing.IndexerOpts{})
	indexed, chunks, errs := 0, 0, 0
	var fileErrors []map[string]string
	for _, f := range body.Files {
		p := cleanRelPath(f.Path)
		if p == "" {
			errs++
			fileErrors = append(fileErrors, map[string]string{"path": f.Path, "error": "invalid path"})
			continue
		}
		if !utf8.ValidString(f.Content) {
			errs++
			fileErrors = append(fileErrors, map[string]string{"path": p, "error": "content is not valid UTF-8 (binary?)"})
			continue
		}
		if len(f.Content) > adminIngestMaxFileBytes {
			errs++
			fileErrors = append(fileErrors, map[string]string{"path": p, "error": "file too large for browser ingest (max 512KiB)"})
			continue
		}
		n, ierr := idx.IndexContent(ctx, proj.ID, p, model, []byte(f.Content))
		if ierr != nil {
			errs++
			fileErrors = append(fileErrors, map[string]string{"path": p, "error": ierr.Error()})
			a.log.Error("ingest index content", "path", p, "err", ierr)
			continue
		}
		indexed++
		chunks += n
	}

	if err := a.store.UpdateProjectStatus(ctx, proj.ID, "ready"); err != nil {
		a.log.Warn("update status after ingest", "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"indexed": indexed, "chunks": chunks, "deleted": deleted, "errors": errs,
		"file_errors": fileErrors,
	})
}

func cleanRelPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return ""
	}
	return p
}
