package webadmin

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"
)

const (
	adminIngestMaxFiles     = 50
	adminIngestMaxFileBytes = 512 * 1024 // 512 KiB per file (browser small-batch)
	adminIngestMaxZipBytes  = 20 * 1024 * 1024
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
	sess, ok := a.loadIngestSession(r.Context(), w, name)
	if !ok {
		return
	}

	var body ingestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, spaErrInvalidJSONBody)
		return
	}
	if status, msg := validateIngestBody(&body); msg != "" {
		writeJSONErr(w, status, msg)
		return
	}

	ctx := r.Context()
	deleted := a.ingestDeletePaths(ctx, sess.proj.ID, body.Delete)
	res := ingestIndexFileList(ctx, a.log, sess.idx, sess.proj.ID, sess.model, body.Files)
	a.finishIngest(ctx, sess.proj.ID, "ingest")
	writeIngestResult(w, res, deleted)
}

// apiProjectArchiveBatch indexes text files from a .zip uploaded by the admin SPA.
// This keeps browser ingest useful for small archive drops without requiring CLI.
func (a *Admin) apiProjectArchiveBatch(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	sess, ok := a.loadIngestSession(r.Context(), w, name)
	if !ok {
		return
	}

	// #nosec G120 -- bounded by adminIngestMaxZipBytes (20 MiB) for browser archive uploads.
	if err := r.ParseMultipartForm(adminIngestMaxZipBytes); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}
	f, fh, err := r.FormFile("archive")
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, "archive file is required")
		return
	}
	defer func() { _ = f.Close() }()

	data, status, msg := readZipUpload(f, fh.Filename)
	if msg != "" {
		writeJSONErr(w, status, msg)
		return
	}
	zr, status, msg := openZipReader(data)
	if msg != "" {
		writeJSONErr(w, status, msg)
		return
	}

	ctx := r.Context()
	res := ingestIndexZipEntries(ctx, a.log, sess.idx, sess.proj.ID, sess.model, zr)
	a.finishIngest(ctx, sess.proj.ID, "ingest archive")
	writeIngestResult(w, res, 0)
}

func (a *Admin) finishIngest(ctx context.Context, projectID int, label string) {
	if err := a.store.UpdateProjectStatus(ctx, projectID, "ready"); err != nil {
		a.log.Warn("update status after "+label, "err", err)
	}
}

func writeIngestResult(w http.ResponseWriter, res ingestIndexResult, deleted int) {
	writeJSON(w, http.StatusOK, map[string]any{
		"indexed": res.indexed, "chunks": res.chunks, "deleted": deleted, "errors": res.errs,
		"file_errors": res.fileErrors,
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

func sanitizeIngestIndexError(err error) string {
	if err == nil {
		return ""
	}
	return "indexing failed for this file"
}
