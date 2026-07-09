package webadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/passwd"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// writeJSON writes a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(headerContentType, "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// --- JSON auth ---------------------------------------------------------------

type loginJSONBody struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	RememberMe bool   `json:"remember_me"`
}

func (a *Admin) apiLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var body loginJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	username := strings.TrimSpace(body.Username)
	password := body.Password
	now := time.Now()
	if !a.limiter.allowed(username, now) {
		writeJSONErr(w, http.StatusTooManyRequests, "too many attempts — wait a few minutes and try again")
		return
	}
	user, err := a.store.GetUserByUsername(r.Context(), username)
	if errors.Is(err, store.ErrNotFound) || (user != nil && user.Disabled) {
		a.limiter.record(username, now)
		writeJSONErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err != nil {
		a.log.Error("login lookup failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	ok, err := passwd.Verify(password, user.PasswordHash)
	if err != nil || !ok {
		a.limiter.record(username, now)
		writeJSONErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	ttl := sessionTTL
	if body.RememberMe {
		ttl = rememberMeTTL
	}
	plaintext, hash, err := newSessionToken()
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	if err := a.store.CreateSession(r.Context(), hash, user.ID, now.Add(ttl)); err != nil {
		a.log.Error("create session failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	a.limiter.reset(username)
	a.setSession(w, plaintext, ttl)
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{"id": user.ID, "username": user.Username, "role": user.Role},
		"csrf": a.csrfToken(plaintext),
	})
}

func (a *Admin) apiLogout(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	if err := a.store.DeleteSession(r.Context(), hashToken(ac.session)); err != nil {
		a.log.Error("delete session failed", "err", err)
	}
	a.clearSession(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *Admin) apiMe(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{"id": ac.user.ID, "username": ac.user.Username, "role": ac.user.Role},
		"csrf": ac.csrf,
	})
}

func (a *Admin) apiSystem(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	caps := []string{"search", "projects", "reindex", "jobs", "git_clone", "files", "explore"}
	if a.chat != nil {
		caps = append(caps, "chat")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"product":       "semidx",
		"mode":          "server",
		"storage":       "PostgreSQL / pgvector",
		"version":       "embedded-admin",
		"caps":          caps,
		"chat_enabled":  a.chat != nil,
		"cli_hints": []string{
			"semidx login <url> --token …",
			"semidx push --project .",
			"semidx index --to-server --project .",
			"semidx --local index --project .",
			"semidx logout",
		},
	})
}

// --- projects / jobs / search JSON -------------------------------------------

type createProjectBody struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	SourceType string `json:"source_type"`
	GitURL     string `json:"git_url"`
	Branch     string `json:"branch"`
	Index      bool   `json:"index"`
}

func (a *Admin) apiCreateProject(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	var body createProjectBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.SourceType = strings.TrimSpace(body.SourceType)
	if body.SourceType == "" {
		body.SourceType = "git"
	}
	if body.SourceType != "git" {
		writeJSONErr(w, http.StatusBadRequest, "only source_type=git is supported in the admin UI (use semidx push for file uploads)")
		return
	}
	body.GitURL = strings.TrimSpace(body.GitURL)
	if body.GitURL == "" {
		writeJSONErr(w, http.StatusBadRequest, "git_url is required")
		return
	}
	if body.Model == "" {
		body.Model = "bge-m3"
	}
	if body.Branch == "" {
		body.Branch = "main"
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = strings.TrimSuffix(path.Base(strings.TrimRight(body.GitURL, "/")), ".git")
	}
	if name == "" || name == "." || name == "/" {
		writeJSONErr(w, http.StatusBadRequest, "could not derive project name — set name explicitly")
		return
	}

	proj, err := a.store.CreateProject(r.Context(), name, body.Model, body.SourceType, body.GitURL, body.Branch, 0)
	if errors.Is(err, store.ErrProjectExists) {
		writeJSONErr(w, http.StatusConflict, "project already exists")
		return
	}
	if err != nil {
		a.log.Error("create project failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not create project")
		return
	}

	out := map[string]any{
		"project": projectToItem(*proj),
	}
	if body.Index {
		jobID, jerr := a.store.EnqueueJob(r.Context(), proj.ID, "full")
		if jerr != nil {
			a.log.Error("enqueue job failed", "err", jerr)
			writeJSONErr(w, http.StatusInternalServerError, "project created but reindex failed to queue")
			return
		}
		out["job_id"] = jobID
	}
	writeJSON(w, http.StatusCreated, out)
}

func (a *Admin) apiDeleteProject(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	if name == "" {
		writeJSONErr(w, http.StatusBadRequest, "project required")
		return
	}
	if err := a.store.DeleteProject(r.Context(), name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "project not found")
			return
		}
		a.log.Error("delete project failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not delete project")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *Admin) apiProjectStatus(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	proj, err := a.store.GetProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		a.log.Error("get project failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	hashes, err := a.store.ListFileHashes(r.Context(), proj.ID)
	if err != nil {
		a.log.Error("list file hashes failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": proj.Name, "identity": proj.Identity, "path": proj.Path,
		"model": proj.Model, "status": proj.Status, "source_type": proj.SourceType,
		"git_url": proj.GitURL, "branch": proj.Branch, "total_files": len(hashes),
	})
}

type reindexBody struct {
	Type string `json:"type"`
}

func (a *Admin) apiReindex(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	var body reindexBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Type == "" {
		body.Type = "full"
	}
	if body.Type != "full" && body.Type != "git_history" {
		writeJSONErr(w, http.StatusBadRequest, "type must be full or git_history")
		return
	}
	proj, err := a.store.GetProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		a.log.Error("get project failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	jobID, err := a.store.EnqueueJob(r.Context(), proj.ID, body.Type)
	if err != nil {
		a.log.Error("enqueue job failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not queue job")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID, "status": "queued"})
}

func (a *Admin) apiGetJob(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		writeJSONErr(w, http.StatusBadRequest, "invalid job id")
		return
	}
	job, err := a.store.GetJob(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		a.log.Error("get job failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": job.ID, "type": job.Type, "status": job.Status, "error": job.Error,
		"files_indexed": job.FilesIndexed, "chunks_created": job.ChunksCreated,
		"deleted_files": job.DeletedFiles, "error_count": job.ErrorCount,
	})
}

type searchJSONBody struct {
	Query   string `json:"query"`
	Project string `json:"project"`
	All     bool   `json:"all"`
	Top     int    `json:"top"`
}

func (a *Admin) apiSearch(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	var body searchJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Query = strings.TrimSpace(body.Query)
	if body.Query == "" {
		writeJSONErr(w, http.StatusBadRequest, "query is required")
		return
	}
	topK := body.Top
	if topK <= 0 || topK > 100 {
		topK = 10
	}

	if body.All {
		d := &searchData{Query: body.Query, AllProjects: true, Top: topK, Ran: true}
		if err := a.searchAllProjects(r.Context(), d, topK); err != nil {
			writeJSONErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"results":       hitsToJSON(d.Results),
			"fallback":      d.Fallback,
			"project_count": d.ProjectCount,
		})
		return
	}

	project := strings.TrimSpace(body.Project)
	if project == "" {
		writeJSONErr(w, http.StatusBadRequest, "project is required unless all=true")
		return
	}
	resp, err := a.search.Search(r.Context(), search.Request{Project: project, Query: body.Query, TopK: topK})
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		a.log.Error("search failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "search failed")
		return
	}
	hits := make([]adminSearchHit, 0, len(resp.Results))
	for _, h := range resp.Results {
		hits = append(hits, adminSearchHit{SearchResult: h})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results":          hitsToJSON(hits),
		"fallback":         resp.Fallback,
		"resolved_project": resp.Project.Name,
	})
}

func hitsToJSON(hits []adminSearchHit) []map[string]any {
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		out = append(out, map[string]any{
			"project":    h.Project,
			"path":       h.FilePath,
			"start_line": h.StartLine,
			"end_line":   h.EndLine,
			"score":      h.Score,
			"content":    h.Content,
		})
	}
	return out
}
