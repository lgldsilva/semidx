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
	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/tenant"
)

// ensure strconv used in this file (apiListAllJobs).

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
		writeJSONErr(w, http.StatusBadRequest, spaErrInvalidJSONBody)
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
		writeJSONErr(w, http.StatusUnauthorized, spaErrInvalidCredentials)
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
		writeJSONErr(w, http.StatusUnauthorized, spaErrInvalidCredentials)
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
	tenantID := tenant.ID(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"user":      map[string]any{"id": ac.user.ID, "username": ac.user.Username, "role": ac.user.Role},
		"tenant_id": tenantID,
		"csrf":      ac.csrf,
	})
}

func (a *Admin) apiSystem(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	caps := []string{"search", "projects", "reindex", "jobs", "git_clone", "files", "explore"}
	if a.chat != nil {
		caps = append(caps, "chat")
	}
	if a.githubToken != "" {
		caps = append(caps, "github_discovery")
	}
	if _, ok := a.store.(store.ConversationStore); ok {
		caps = append(caps, "conversations")
	}
	if a.credMgr().Supported() && a.secrets != nil && a.secrets.Enabled() {
		caps = append(caps, "git_credentials")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"product":      "semidx",
		"mode":         "server",
		"storage":      "PostgreSQL / pgvector",
		"version":      "embedded-admin",
		"caps":         caps,
		"chat_enabled": a.chat != nil,
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
	Name        string                   `json:"name"`
	Model       string                   `json:"model"`
	SourceType  string                   `json:"source_type"`
	GitURL      string                   `json:"git_url"`
	Branch      string                   `json:"branch"`
	Index       bool                     `json:"index"`
	PrivacyMode string                   `json:"privacy_mode"`
	Credential  *inlineProjectCredential `json:"credential"`
}

// normalizeCreateProjectBody validates and normalizes a create-project payload.
// On error it returns the HTTP status and message to send.
func normalizeCreateProjectBody(body *createProjectBody) (name string, status int, msg string) {
	body.SourceType = strings.TrimSpace(body.SourceType)
	if body.SourceType == "" {
		body.SourceType = "git"
	}
	if body.SourceType != "git" && body.SourceType != "push" {
		return "", http.StatusBadRequest, "source_type must be git or push"
	}
	if body.Model == "" {
		body.Model = "bge-m3"
	}
	if _, err := privacy.NormalizeMode(body.PrivacyMode); err != nil {
		return "", http.StatusBadRequest, err.Error()
	}
	name = strings.TrimSpace(body.Name)
	body.GitURL = strings.TrimSpace(body.GitURL)
	body.Branch = strings.TrimSpace(body.Branch)
	if body.SourceType == "git" {
		return normalizeGitProjectBody(body, name)
	}
	return normalizePushProjectBody(body, name)
}

func normalizeGitProjectBody(body *createProjectBody, name string) (string, int, string) {
	if body.GitURL == "" {
		return "", http.StatusBadRequest, "git_url is required for source_type=git"
	}
	if body.Branch == "" {
		body.Branch = "main"
	}
	if name == "" {
		name = strings.TrimSuffix(path.Base(strings.TrimRight(body.GitURL, "/")), ".git")
	}
	return validateProjectName(name)
}

func normalizePushProjectBody(body *createProjectBody, name string) (string, int, string) {
	body.GitURL = ""
	body.Branch = ""
	body.Index = false
	if name == "" {
		return "", http.StatusBadRequest, "name is required for source_type=push"
	}
	return validateProjectName(name)
}

func validateProjectName(name string) (string, int, string) {
	if name == "" || name == "." || name == "/" {
		return "", http.StatusBadRequest, "could not derive project name — set name explicitly"
	}
	return name, 0, ""
}

func validateCreateProjectCredential(w http.ResponseWriter, ac *authCtx, body *createProjectBody) bool {
	if body.Credential == nil || strings.TrimSpace(body.Credential.Secret) == "" {
		return true
	}
	if ac.user.Role != "admin" {
		writeJSONErr(w, http.StatusForbidden, "admin role required to set a git credential")
		return false
	}
	if body.SourceType != "git" {
		writeJSONErr(w, http.StatusBadRequest, "credential is only supported for source_type=git")
		return false
	}
	return true
}

func (a *Admin) apiCreateProject(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	var body createProjectBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, spaErrInvalidJSONBody)
		return
	}
	name, status, msg := normalizeCreateProjectBody(&body)
	if msg != "" {
		writeJSONErr(w, status, msg)
		return
	}
	if !validateCreateProjectCredential(w, ac, &body) {
		return
	}
	if err := enforceProjectQuota(r.Context(), a.store); err != nil {
		if errors.Is(err, errTenantQuotaExceeded) {
			writeJSONErr(w, http.StatusTooManyRequests, err.Error())
			return
		}
		a.log.Error("project quota lookup failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not evaluate tenant quota")
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
	if policy, ok := a.store.(store.ProjectPolicyStore); ok {
		mode, _ := privacy.NormalizeMode(body.PrivacyMode)
		if err := policy.SetProjectPrivacy(r.Context(), proj.ID, string(mode)); err != nil {
			a.log.Error("set project privacy failed", "err", err)
			writeJSONErr(w, http.StatusInternalServerError, "could not save project privacy policy")
			return
		}
		proj.PrivacyMode = string(mode)
	}

	if err := a.createInlineProjectCredential(r.Context(), proj.ID, body.Credential); err != nil {
		writeGitCredErr(w, a.log, "create project credential", err)
		return
	}

	out := map[string]any{
		"project": projectToItem(*proj),
	}
	if body.Index && body.SourceType == "git" {
		jobID, jerr := a.store.EnqueueJob(r.Context(), proj.ID, "full")
		if jerr != nil {
			a.log.Error("enqueue job failed", "err", jerr)
			writeJSONErr(w, http.StatusInternalServerError, "project created but reindex failed to queue")
			return
		}
		out["job_id"] = jobID
	}
	if body.SourceType == "push" {
		out["push_hint"] = "semidx login <url> --token <key> && semidx push --project . --name " + name
	}
	writeJSON(w, http.StatusCreated, out)
}

func (a *Admin) apiListAllJobs(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	limit, offset := parseListParams(r)
	if limit == 0 {
		limit = 20
	}
	fetchLimit := limit + offset
	if fetchLimit < limit {
		fetchLimit = limit
	}
	jobs, err := a.store.ListRecentJobs(r.Context(), 0, fetchLimit)
	if err != nil {
		a.log.Error("list all jobs failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	jobs = paginateJobs(jobs, limit, offset)
	// Attach project names when possible.
	type jobRow struct {
		jobItem
		ProjectID   int    `json:"project_id"`
		ProjectName string `json:"project_name,omitempty"`
	}
	// Build id→name map once.
	projects, _ := a.store.ListProjects(r.Context(), 0, 0)
	names := map[int]string{}
	for _, p := range projects {
		names[p.ID] = p.Name
	}
	rows := make([]jobRow, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, jobRow{
			jobItem: jobToItem(j), ProjectID: j.ProjectID, ProjectName: names[j.ProjectID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": rows, "limit": limit, "offset": offset})
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
			writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
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
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
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
		"git_url": proj.GitURL, "branch": proj.Branch, "privacy_mode": proj.PrivacyMode, "total_files": len(hashes),
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
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
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
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	if project == "" {
		writeJSONErr(w, http.StatusBadRequest, "project query parameter is required")
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		writeJSONErr(w, http.StatusBadRequest, "invalid job id")
		return
	}
	a.writeScopedJob(w, r, project, id)
}

func (a *Admin) apiProjectJob(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	project := r.PathValue("project")
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		writeJSONErr(w, http.StatusBadRequest, "invalid job id")
		return
	}
	a.writeScopedJob(w, r, project, id)
}

func (a *Admin) writeScopedJob(w http.ResponseWriter, r *http.Request, project string, id int) {
	job, err := a.store.GetJob(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrJobNotFound)
		return
	}
	if err != nil {
		a.log.Error("get job failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	proj, err := a.store.GetProjectByID(r.Context(), job.ProjectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, spaErrJobNotFound)
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	if proj.Name != project {
		writeJSONErr(w, http.StatusNotFound, spaErrJobNotFound)
		return
	}
	writeJSON(w, http.StatusOK, jobToJSON(job))
}

// jobErrorSummary maps a failed job's raw error (which may contain DSNs, hosts
// or credentials) to a safe, actionable summary for the authenticated admin API.
// Only curated strings are returned — no raw error text — so nothing sensitive
// leaks; the full detail stays in the server logs.
func jobErrorSummary(raw string) string {
	l := strings.ToLower(raw)
	// Classify git failures only within a git context, so a DB error like
	// "pq: password authentication failed" is NOT mistaken for a git auth error.
	isGit := strings.Contains(l, "git clone") || strings.Contains(l, "git pull") ||
		strings.Contains(l, "cloning into") || strings.Contains(l, "fatal:") ||
		strings.Contains(l, "remote:") || strings.Contains(l, "git credential")
	if isGit {
		switch {
		case strings.Contains(l, "git credential"):
			return "git sync failed: the stored git credential could not be used (SEMIDX_SECRET_KEY missing/changed, or lookup failed) — see server logs"
		case strings.Contains(l, "host key verification failed"):
			return "git clone failed: SSH host key verification failed — the remote's host key changed or does not match the credential's known_hosts"
		case strings.Contains(l, "publickey"):
			return "git clone failed: SSH key rejected (permission denied, publickey) — check the credential's private key and the repo's access rights"
		case strings.Contains(l, "cannot run ssh") || strings.Contains(l, "unable to fork"):
			return "git clone failed: the server has no SSH client — register the repo with an https:// URL"
		case strings.Contains(l, "ssl certificate") || strings.Contains(l, "certificate problem") || strings.Contains(l, "unable to get local issuer"):
			return "git clone failed: TLS certificate not trusted (self-signed?) — set SEMIDX_GIT_SSL_NO_VERIFY=true on the server for self-signed hosts"
		case strings.Contains(l, "authentication failed") || strings.Contains(l, "could not read username") || strings.Contains(l, "invalid username or password") || strings.Contains(l, "403"):
			return "git clone failed: authentication required or invalid — check the repo credentials"
		case strings.Contains(l, "not found") || strings.Contains(l, "404"):
			return "git clone failed: repository not found or no access"
		default:
			return "git clone/pull failed — see server logs"
		}
	}
	if strings.Contains(l, "embedding") || strings.Contains(l, "model info") || strings.Contains(l, "ensure chunks") {
		return "indexing failed: embedding/store error — see server logs"
	}
	return "index job failed — see server logs"
}

func jobToJSON(j *store.Job) map[string]any {
	errMsg := j.Error
	if j.Status == "failed" && errMsg != "" {
		// The raw error may carry DSNs, hosts or credentials, so don't expose it
		// verbatim. Map it to a SAFE, ACTIONABLE summary so the admin learns why
		// a reindex failed (e.g. ssh/cert/auth) without leaking secrets; the full
		// detail stays in the server logs. (A bare "index job failed" hid the
		// cause entirely.)
		errMsg = jobErrorSummary(errMsg)
	}
	out := map[string]any{
		"id": j.ID, "type": j.Type, "status": j.Status, "error": errMsg,
		"files_indexed": j.FilesIndexed, "chunks_created": j.ChunksCreated,
		"deleted_files": j.DeletedFiles, "error_count": j.ErrorCount,
		"progress_done": j.ProgressDone, "progress_total": j.ProgressTotal,
	}
	if j.ProgressTotal > 0 {
		pct := j.ProgressDone * 100 / j.ProgressTotal
		if pct > 100 {
			pct = 100
		}
		out["progress_percent"] = pct
	}
	return out
}

func paginateJobs(jobs []store.Job, limit, offset int) []store.Job {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(jobs) {
		return []store.Job{}
	}
	if limit <= 0 {
		return jobs[offset:]
	}
	end := offset + limit
	if end > len(jobs) {
		end = len(jobs)
	}
	return jobs[offset:end]
}

type searchJSONBody struct {
	Query      string `json:"query"`
	Project    string `json:"project"`
	All        bool   `json:"all"`
	Top        int    `json:"top"`
	Graph      bool   `json:"graph"`
	GraphDepth int    `json:"graph_depth"`
}

func (a *Admin) apiSearch(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	var body searchJSONBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, spaErrInvalidJSONBody)
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
	body.GraphDepth = search.ClampGraphDepth(body.GraphDepth)

	if body.All {
		d := &searchData{Query: body.Query, AllProjects: true, Top: topK, Ran: true}
		if err := a.searchAllProjects(r.Context(), d, topK); err != nil {
			// Infra failures are collapsed to a safe sentinel upstream
			// (REQ-SRCH-08) and reported as 500; the remaining errors are
			// intentional, safe user messages (e.g. "no indexed projects").
			if errors.Is(err, errSearchFailed) {
				writeJSONErr(w, http.StatusInternalServerError, errSearchFailed.Error())
				return
			}
			writeJSONErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"results":        hitsToJSON(d.Results),
			"fallback":       d.Fallback,
			"degraded":       d.Degraded,
			"retry_after_ms": d.RetryAfter.Milliseconds(),
			"project_count":  d.ProjectCount,
		})
		return
	}

	project := strings.TrimSpace(body.Project)
	if project == "" {
		writeJSONErr(w, http.StatusBadRequest, "project is required unless all=true")
		return
	}
	req := search.Request{
		Project: project, Query: body.Query, TopK: topK,
		Graph: body.Graph, GraphMaxDepth: body.GraphDepth,
	}
	resp, err := a.search.Search(r.Context(), req)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
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
		"degraded":         resp.Degraded,
		"retry_after_ms":   resp.RetryAfter.Milliseconds(),
		"resolved_project": resp.Project.Name,
		"graph":            body.Graph,
	})
}

func hitsToJSON(hits []adminSearchHit) []map[string]any {
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		out = append(out, map[string]any{
			"project":      h.Project,
			"path":         h.FilePath,
			"start_line":   h.StartLine,
			"end_line":     h.EndLine,
			"score":        h.Score,
			"fusion_score": h.FusionScore,
			"source_rank":  h.SourceRank,
			"content":      h.Content,
		})
	}
	return out
}
