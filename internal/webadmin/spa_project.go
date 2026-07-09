package webadmin

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/store"
)

// projectItem is the JSON shape for project list/detail (enriched).
type projectItem struct {
	Name         string         `json:"name"`
	Identity     string         `json:"identity,omitempty"`
	Path         string         `json:"path,omitempty"`
	Model        string         `json:"model"`
	Status       string         `json:"status"`
	SourceType   string         `json:"source_type,omitempty"`
	GitURL       string         `json:"git_url,omitempty"`
	Branch       string         `json:"branch,omitempty"`
	Dims         int            `json:"dims,omitempty"`
	License      string         `json:"license,omitempty"`
	TotalFiles   int            `json:"total_files"`
	TotalChunks  *int           `json:"total_chunks,omitempty"`
	LastCommit   string         `json:"last_commit,omitempty"`
	LastJob      *jobItem       `json:"last_job,omitempty"`
	ExtBreakdown map[string]int `json:"ext_breakdown,omitempty"`
}

type jobItem struct {
	ID            int    `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	FilesIndexed  int    `json:"files_indexed,omitempty"`
	ChunksCreated int    `json:"chunks_created,omitempty"`
	DeletedFiles  int    `json:"deleted_files,omitempty"`
	ErrorCount    int    `json:"error_count,omitempty"`
}

type fileEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type fileChunk struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content"`
}

func jobToItem(j store.Job) jobItem {
	return jobItem{
		ID: j.ID, Type: j.Type, Status: j.Status, Error: j.Error,
		FilesIndexed: j.FilesIndexed, ChunksCreated: j.ChunksCreated,
		DeletedFiles: j.DeletedFiles, ErrorCount: j.ErrorCount,
	}
}

func projectToItem(p store.Project) projectItem {
	return projectItem{
		Name: p.Name, Identity: p.Identity, Path: p.Path, Model: p.Model,
		Status: p.Status, SourceType: p.SourceType, GitURL: p.GitURL,
		Branch: p.Branch, Dims: p.Dims, License: p.LicenseSPDXID,
	}
}

func (a *Admin) enrichProject(ctx context.Context, p store.Project, withChunks, withExt bool) projectItem {
	item := projectToItem(p)
	if n, err := a.store.CountProjectFiles(ctx, p.ID); err == nil {
		item.TotalFiles = n
	}
	if sha, err := a.store.GetProjectCommit(ctx, p.ID); err == nil {
		item.LastCommit = sha
	}
	if jobs, err := a.store.ListRecentJobs(ctx, p.ID, 1); err == nil && len(jobs) > 0 {
		j := jobToItem(jobs[0])
		item.LastJob = &j
	}
	if withChunks {
		dims := p.Dims
		if dims <= 0 {
			dims = 0
		}
		if n, err := countChunks(ctx, a.store, p.ID, dims); err == nil {
			item.TotalChunks = &n
		}
	}
	if withExt {
		if hashes, err := a.store.ListFileHashes(ctx, p.ID); err == nil {
			item.ExtBreakdown = extBreakdown(hashes)
		}
	}
	return item
}

type chunkCounter interface {
	CountProjectChunks(ctx context.Context, projectID, dims int) (int, error)
}

func countChunks(ctx context.Context, st store.Store, projectID, dims int) (int, error) {
	if cc, ok := st.(chunkCounter); ok {
		return cc.CountProjectChunks(ctx, projectID, dims)
	}
	return 0, errors.New("chunk count not supported")
}

func extBreakdown(hashes map[string]string) map[string]int {
	out := map[string]int{}
	for path := range hashes {
		ext := "other"
		if i := strings.LastIndex(path, "."); i >= 0 && i < len(path)-1 {
			cand := strings.ToLower(path[i+1:])
			if len(cand) <= 12 && !strings.Contains(cand, "/") {
				ext = cand
			}
		}
		out[ext]++
	}
	return out
}

// projectsAPI lists projects with file counts and last job (list view).
func (a *Admin) projectsAPI(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	limit, offset := parseListParams(r)
	projects, err := a.store.ListProjects(r.Context(), limit, offset)
	if err != nil {
		a.log.Error("list projects (api) failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	items := make([]projectItem, 0, len(projects))
	for _, p := range projects {
		items = append(items, a.enrichProject(r.Context(), p, false, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": items})
}

func (a *Admin) apiProjectDetail(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	proj, err := a.store.GetProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		a.log.Error("get project failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	item := a.enrichProject(r.Context(), *proj, true, true)
	jobs, _ := a.store.ListRecentJobs(r.Context(), proj.ID, 10)
	jobItems := make([]jobItem, 0, len(jobs))
	for _, j := range jobs {
		jobItems = append(jobItems, jobToItem(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": item,
		"jobs":    jobItems,
	})
}

func (a *Admin) apiProjectFiles(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	proj, err := a.store.GetProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	hashes, err := a.store.ListFileHashes(r.Context(), proj.ID)
	if err != nil {
		a.log.Error("list file hashes failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 5000 {
		limit = 2000
	}
	if offset < 0 {
		offset = 0
	}

	paths := make([]string, 0, len(hashes))
	for p := range hashes {
		if prefix != "" && !strings.HasPrefix(p, prefix) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p), q) {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	total := len(paths)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	slice := paths[offset:end]
	files := make([]fileEntry, 0, len(slice))
	for _, p := range slice {
		files = append(files, fileEntry{Path: p, Hash: hashes[p]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":  files,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (a *Admin) apiProjectFileContent(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		writeJSONErr(w, http.StatusBadRequest, "path is required")
		return
	}
	// Prevent path traversal tricks in the index key.
	if strings.Contains(filePath, "..") || strings.HasPrefix(filePath, "/") {
		writeJSONErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	proj, err := a.store.GetProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	dims := proj.Dims
	if dims <= 0 {
		dims = 1024
	}
	const maxChunks = 64
	chunks, err := a.store.FetchChunksByPath(r.Context(), proj.ID, filePath, dims, maxChunks)
	if err != nil {
		// dims mismatch: try keyword dims then common sizes
		for _, d := range []int{store.KeywordDims, 768, 1024, 1536, 3072} {
			if d == dims {
				continue
			}
			chunks, err = a.store.FetchChunksByPath(r.Context(), proj.ID, filePath, d, maxChunks)
			if err == nil && len(chunks) > 0 {
				dims = d
				break
			}
		}
		if err != nil {
			a.log.Error("fetch chunks failed", "err", err, "path", filePath)
			writeJSONErr(w, http.StatusInternalServerError, "could not load file content")
			return
		}
	}
	outChunks := make([]fileChunk, 0, len(chunks))
	var b strings.Builder
	for i, c := range chunks {
		outChunks = append(outChunks, fileChunk{
			StartLine: c.StartLine, EndLine: c.EndLine, Content: c.Content,
		})
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(c.Content)
	}
	truncated := len(chunks) >= maxChunks
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      filePath,
		"dims":      dims,
		"chunks":    outChunks,
		"content":   b.String(),
		"truncated": truncated,
	})
}

func (a *Admin) apiProjectJobs(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	proj, err := a.store.GetProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	limit, offset := parseListParams(r)
	fetchLimit := limit
	if fetchLimit == 0 {
		fetchLimit = 20
	}
	fetchLimit += offset
	if fetchLimit < 0 {
		fetchLimit = 20
	}
	jobs, err := a.store.ListRecentJobs(r.Context(), proj.ID, fetchLimit)
	if err != nil {
		a.log.Error("list jobs failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	jobs = paginateJobs(jobs, limit, offset)
	items := make([]jobItem, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, jobToItem(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": items, "limit": limit, "offset": offset})
}
