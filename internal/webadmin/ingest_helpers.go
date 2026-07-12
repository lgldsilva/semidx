package webadmin

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

type ingestSession struct {
	proj  *store.Project
	model string
	dims  int
	idx   *indexing.Indexer
}

type ingestIndexResult struct {
	indexed, chunks, errs int
	fileErrors            []map[string]string
}

// loadIngestSession resolves the project, embedder, and indexer for browser ingest.
func (a *Admin) loadIngestSession(ctx context.Context, w http.ResponseWriter, name string) (*ingestSession, bool) {
	proj, err := a.store.GetProject(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return nil, false
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return nil, false
	}
	if a.emb == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "no embedder configured on server")
		return nil, false
	}
	model := proj.Model
	if model == "" {
		model = "bge-m3"
	}
	info, err := a.emb.ModelInfo(ctx, model)
	if err != nil {
		a.log.Warn("model info for ingest", "err", err)
		writeJSONErr(w, http.StatusBadGateway, "embedding model unavailable — configure a provider or use keyword mode on CLI")
		return nil, false
	}
	dims := info.Dims
	if dims <= 0 {
		dims = 1024
	}
	if err := a.store.EnsureChunksTable(ctx, dims); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "could not prepare storage")
		return nil, false
	}
	idx := indexing.NewIndexer(a.store, a.emb, dims, indexing.IndexerOpts{})
	return &ingestSession{proj: proj, model: model, dims: dims, idx: idx}, true
}

func validateIngestBody(body *ingestBody) (int, string) {
	if len(body.Files) == 0 && len(body.Delete) == 0 {
		return http.StatusBadRequest, "files or delete required"
	}
	if len(body.Files) > adminIngestMaxFiles {
		return http.StatusBadRequest, "too many files (max 50 per batch)"
	}
	return 0, ""
}

func (a *Admin) ingestDeletePaths(ctx context.Context, projectID int, paths []string) int {
	deleted := 0
	for _, p := range paths {
		p = cleanRelPath(p)
		if p == "" {
			continue
		}
		if err := a.store.DeleteFileByPath(ctx, projectID, p); err != nil {
			a.log.Error("delete file on ingest", "path", p, "err", err)
			continue
		}
		deleted++
	}
	return deleted
}

func ingestIndexOneFile(ctx context.Context, idx *indexing.Indexer, projectID int, model, rawPath, content string) (chunks int, ferr map[string]string, err error) {
	p := cleanRelPath(rawPath)
	if p == "" {
		return 0, map[string]string{"path": rawPath, "error": "invalid path"}, nil
	}
	if !utf8.ValidString(content) {
		return 0, map[string]string{"path": p, "error": "content is not valid UTF-8 (binary?)"}, nil
	}
	if len(content) > adminIngestMaxFileBytes {
		return 0, map[string]string{"path": p, "error": spaErrFileTooLargeIngest}, nil
	}
	n, err := idx.IndexContent(ctx, projectID, p, model, []byte(content))
	if err != nil {
		return 0, map[string]string{"path": p, "error": sanitizeIngestIndexError(err)}, err
	}
	return n, nil, nil
}

func ingestIndexFileList(ctx context.Context, log *slog.Logger, idx *indexing.Indexer, projectID int, model string, files []ingestFile) ingestIndexResult {
	var res ingestIndexResult
	for _, f := range files {
		n, ferr, ierr := ingestIndexOneFile(ctx, idx, projectID, model, f.Path, f.Content)
		if ferr != nil {
			res.errs++
			res.fileErrors = append(res.fileErrors, ferr)
			if ierr != nil {
				log.Error("ingest index content", "path", ferr["path"], "err", ierr)
			}
			continue
		}
		res.indexed++
		res.chunks += n
	}
	return res
}

func readZipUpload(f io.Reader, filename string) ([]byte, int, string) {
	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		return nil, http.StatusBadRequest, "only .zip archives are supported"
	}
	data, err := io.ReadAll(io.LimitReader(f, adminIngestMaxZipBytes+1))
	if err != nil {
		return nil, http.StatusBadRequest, "could not read uploaded archive"
	}
	if len(data) > adminIngestMaxZipBytes {
		return nil, http.StatusBadRequest, "archive too large (max 20MiB)"
	}
	return data, 0, ""
}

func ingestIndexZipEntries(ctx context.Context, log *slog.Logger, idx *indexing.Indexer, projectID int, model string, zr *zip.Reader) ingestIndexResult {
	var res ingestIndexResult
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		content, ferr := readZipEntry(zf)
		if ferr != nil {
			res.errs++
			res.fileErrors = append(res.fileErrors, ferr)
			continue
		}
		n, ierr, indexErr := ingestIndexOneFile(ctx, idx, projectID, model, zf.Name, string(content))
		if ierr != nil {
			res.errs++
			res.fileErrors = append(res.fileErrors, ierr)
			if indexErr != nil {
				log.Error("ingest archive index content", "path", ierr["path"], "err", indexErr)
			}
			continue
		}
		res.indexed++
		res.chunks += n
	}
	return res
}

func readZipEntry(zf *zip.File) ([]byte, map[string]string) {
	p := cleanRelPath(zf.Name)
	if p == "" {
		return nil, map[string]string{"path": zf.Name, "error": "invalid path"}
	}
	if zf.UncompressedSize64 > adminIngestMaxFileBytes {
		return nil, map[string]string{"path": p, "error": spaErrFileTooLargeIngest}
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, map[string]string{"path": p, "error": "could not read zip entry"}
	}
	content, readErr := io.ReadAll(io.LimitReader(rc, adminIngestMaxFileBytes+1))
	_ = rc.Close()
	if readErr != nil {
		return nil, map[string]string{"path": p, "error": "could not read zip entry"}
	}
	if len(content) > adminIngestMaxFileBytes {
		return nil, map[string]string{"path": p, "error": spaErrFileTooLargeIngest}
	}
	if !utf8.Valid(content) {
		return nil, map[string]string{"path": p, "error": "content is not valid UTF-8 (binary?)"}
	}
	return content, nil
}

func openZipReader(data []byte) (*zip.Reader, int, string) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, http.StatusBadRequest, "invalid zip archive"
	}
	return zr, 0, ""
}
