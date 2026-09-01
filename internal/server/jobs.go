package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lgldsilva/semidx/internal/depcatalog"
	"github.com/lgldsilva/semidx/internal/depresolve"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/gitsync"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/tenant"
)

const errJobNotFound = "job not found"

const staleJobAge = 30 * time.Minute

// StartWorkers launches n background workers that drain queued index jobs until
// ctx is cancelled. Git projects are cloned/pulled into dataDir first.
//
// A single shared LISTEN connection is used for all workers. Each worker used
// to call ListenJobInsert independently, which acquired one pool connection per
// worker and held it forever — with the default pgxpool size (~4) that exhausted
// the pool and hung every authenticated API request (TokenByHash could not
// acquire a connection).
func (s *Server) StartWorkers(ctx context.Context, n int, dataDir string) {
	if n < 1 {
		n = 1
	}
	// Reclaim ephemeral credential material (SSH keys, pinned known_hosts, CA
	// bundles) left behind by syncs that died before their deferred cleanup ran.
	if err := gitsync.SweepTempKeys(dataDir); err != nil {
		s.log.Warn("sweep leftover git credential temp files", "err", err)
	}
	if r, ok := s.store.(interface {
		RequeueStaleJobs(context.Context, time.Duration) (int64, error)
	}); ok {
		if count, err := r.RequeueStaleJobs(ctx, staleJobAge); err != nil {
			s.log.Warn("requeue stale jobs failed", "err", err)
		} else if count > 0 {
			s.jobsQueued.Add(float64(count))
			s.log.Warn("requeued stale jobs after worker restart", "count", count)
		}
	}
	notifyCh := s.openJobNotify(ctx)
	for i := 0; i < n; i++ {
		go s.worker(ctx, dataDir, notifyCh)
	}
	s.log.Info("job workers started", "count", n)
}

// openJobNotify starts one shared LISTEN/NOTIFY channel, or returns nil to fall
// back to polling when the store does not support it / setup fails.
func (s *Server) openJobNotify(ctx context.Context) <-chan string {
	jn, ok := s.store.(store.JobNotifier)
	if !ok {
		return nil
	}
	ch, err := jn.ListenJobInsert(ctx)
	if err != nil {
		s.log.Warn("LISTEN/NOTIFY unavailable, falling back to polling", "err", err)
		return nil
	}
	return ch
}

// waitForJob blocks until a job notification arrives, the poll ticker fires,
// or the context is cancelled.
func (s *Server) waitForJob(ctx context.Context, notifyCh <-chan string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	if notifyCh != nil {
		select {
		case <-notifyCh:
		case <-ticker.C:
		case <-ctx.Done():
		}
	} else {
		select {
		case <-ticker.C:
		case <-ctx.Done():
		}
	}
}

func (s *Server) worker(ctx context.Context, dataDir string, notifyCh <-chan string) {
	for {
		// Drain all currently-queued jobs before sleeping again.
		for s.claimAndRun(ctx, dataDir) {
			if ctx.Err() != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}

		// No job available — wait for notification or ticker.
		s.waitForJob(ctx, notifyCh)
	}
}

// claimAndRun processes one queued job; returns true if a job was claimed.
func (s *Server) claimAndRun(ctx context.Context, dataDir string) bool {
	job, err := s.store.ClaimJob(ctx)
	if err != nil {
		s.log.Error("claim job", "err", err)
		return false
	}
	if job == nil {
		return false
	}
	s.jobsQueued.Dec()
	tenantID := job.TenantID
	if tenantID <= 0 {
		// Store fakes and jobs created before migration 00020 have no tenant
		// field; retain the single-tenant worker behavior for those callers.
		tenantID = tenant.DefaultID
	}
	jobCtx, err := tenant.With(ctx, tenant.Context{ID: tenantID})
	if err != nil {
		s.log.Error("invalid job tenant", "job", job.ID, "tenant", job.TenantID, "err", err)
		_ = s.store.FailJob(ctx, job.ID, err.Error())
		return true
	}
	s.runJob(jobCtx, job, dataDir)
	return true
}

func (s *Server) runJob(ctx context.Context, job *store.Job, dataDir string) {
	s.jobsRunning.Inc()
	defer s.jobsRunning.Dec()

	fail := s.jobFailure(ctx, job)

	proj, err := s.store.GetProjectByID(ctx, job.ProjectID)
	if err != nil {
		fail("project not found: " + err.Error())
		return
	}

	if s.runSpecialJob(ctx, job, proj, dataDir, fail) {
		return
	}
	s.runIndexJob(ctx, job, proj, dataDir, fail)
}

func (s *Server) jobFailure(ctx context.Context, job *store.Job) func(string) {
	return func(msg string) {
		s.log.Error("index job failed", "job", job.ID, "err", msg)
		s.jobsTotal.WithLabelValues(job.Type, "failed").Inc()
		if err := s.store.FailJob(ctx, job.ID, msg); err != nil {
			s.log.Error("mark job failed", "job", job.ID, "err", err)
		}
		if err := s.store.UpdateProjectStatus(ctx, job.ProjectID, "error"); err != nil {
			s.log.Warn("mark project error", "job", job.ID, "err", err)
		}
	}
}

func (s *Server) runSpecialJob(ctx context.Context, job *store.Job, proj *store.Project, dataDir string, fail func(string)) bool {
	// Batch jobs carry their payload inline; skip git sync / project path.
	switch job.Type {
	case "batch":
		s.runBatchJob(ctx, job, proj)
		return true
	case "resolve_dependencies":
		s.runDependencyResolveJob(ctx, job, proj, dataDir, fail)
		return true
	default:
		return false
	}
}

func (s *Server) runIndexJob(ctx context.Context, job *store.Job, proj *store.Project, dataDir string, fail func(string)) {

	path, failMsg := s.resolveJobIndexPath(ctx, proj, dataDir)
	if failMsg != "" {
		fail(failMsg)
		return
	}
	if path == "" {
		fail("project has no indexable source path (push projects upload via files/batch)")
		return
	}
	if proj.SourceType == "git" && path != proj.Path {
		if err := s.store.UpdateProjectPath(ctx, job.ProjectID, path); err != nil {
			s.log.Warn("update project path", "project", proj.Name, "path", path, "err", err)
		}
	}

	modelCtx := ctx
	privacyMode, modeErr := privacy.NormalizeMode(proj.PrivacyMode)
	if modeErr != nil {
		fail("invalid project privacy policy")
		return
	}
	if privacyMode == privacy.Edge {
		modelCtx = embed.WithForceLocal(ctx, true)
	}
	info, err := s.emb.ModelInfo(modelCtx, proj.Model)
	if err != nil {
		fail("model info: " + err.Error())
		return
	}
	if err := s.store.EnsureChunksTable(ctx, info.Dims); err != nil {
		fail("ensure chunks table: " + err.Error())
		return
	}

	opts := s.indexerOptsForJob(job.Type, func(done, total, filesIndexed, chunksCreated, errorCount int) {
		if err := s.store.UpdateJobProgress(ctx, job.ID, done, total, filesIndexed, chunksCreated, errorCount); err != nil {
			s.log.Warn("update job progress", "job", job.ID, "err", err)
		}
	})
	opts.PrivacyMode = privacyMode
	idx := indexing.NewIndexer(s.store, s.emb, info.Dims, opts)
	if isForceJob(job.Payload) {
		idx.SetForce(true)
	}
	if proj.SourceType == "git" && job.Type != "git_history" {
		idx.SetWorktree(path)
	}
	stats, err := idx.IndexProject(ctx, job.ProjectID, path, proj.Model, s.indexLimits.MaxFilesPerProject)
	if err != nil {
		fail("index: " + err.Error())
		return
	}
	if err := s.store.CompleteJob(ctx, job.ID, stats.FilesIndexed, stats.ChunksCreated, 0, stats.Errors); err != nil {
		s.log.Error("mark job complete", "job", job.ID, "err", err)
		fail("complete index job: " + err.Error())
		return
	}
	s.jobsTotal.WithLabelValues(job.Type, "succeeded").Inc()
	s.log.Info("index job done", "job", job.ID, "project", proj.Name,
		"files", stats.FilesIndexed, "chunks", stats.ChunksCreated)
}

// runDependencyResolveJob executes native package-manager tooling in a
// managed worker and refreshes the same catalog used by customer agents. The
// job is deliberately separate from indexing because resolution may access a
// package registry or mutate a package-manager workspace.
func (s *Server) runDependencyResolveJob(ctx context.Context, job *store.Job, proj *store.Project, dataDir string, fail func(string)) {
	depStore, ok := s.store.(store.DependencyStore)
	if !ok {
		fail("dependency catalog unavailable")
		return
	}
	path, failMsg := s.resolveJobIndexPath(ctx, proj, dataDir)
	if failMsg != "" {
		fail(failMsg)
		return
	}
	if path == "" {
		fail("project has no local source path; use a customer agent")
		return
	}
	resolved, err := depresolve.New().ResolveProject(ctx, path)
	if err != nil {
		fail(err.Error())
		return
	}
	deps := make([]store.Dependency, 0, len(resolved))
	for _, dep := range resolved {
		deps = append(deps, dependencyFromCatalog(dep))
	}
	if err := depStore.ReplaceProjectDependencies(ctx, proj.ID, deps); err != nil {
		fail("store dependencies: " + err.Error())
		return
	}
	if err := s.store.CompleteJob(ctx, job.ID, 0, 0, 0, 0); err != nil {
		s.log.Error("mark dependency job complete", "job", job.ID, "err", err)
		fail("complete dependency job: " + err.Error())
		return
	}
	s.jobsTotal.WithLabelValues(job.Type, "succeeded").Inc()
	s.log.Info("dependency resolution job done", "job", job.ID, "project", proj.Name, "dependencies", len(deps))
}

func dependencyFromCatalog(dep depcatalog.Dependency) store.Dependency {
	return store.Dependency{
		Ecosystem: string(dep.Ecosystem), Name: dep.Name, NormalizedName: dep.NormalizedName,
		Constraint: dep.Constraint, ResolvedVersion: dep.ResolvedVersion, Scope: dep.Scope,
		Source: dep.Source, Manifest: dep.Manifest, Direct: dep.Direct,
	}
}

// resolveJobIndexPath returns the filesystem path to index for proj.
// failMsg is non-empty when git sync failed (safe to persist on the job).
func (s *Server) resolveJobIndexPath(ctx context.Context, proj *store.Project, dataDir string) (path, failMsg string) {
	path = proj.Path
	if proj.SourceType != "git" {
		return path, ""
	}
	gitOpts, err := s.resolveGitOptions(ctx, proj, dataDir)
	if err != nil {
		return "", err.Error()
	}
	p, err := gitsync.SyncWithOptions(ctx, gitOpts)
	if err != nil {
		return "", err.Error()
	}
	return p, ""
}

// runBatchJob processes a batch (push) job: deserialises the JSON payload
// embedded in the job, chunks/embeds files, and completes the job with the
// result counts. On a fatal error (model unavailable, bad payload) it calls
// FailJob instead.
func (s *Server) runBatchJob(ctx context.Context, job *store.Job, proj *store.Project) {
	fail := func(msg string) {
		if err := s.store.FailJob(ctx, job.ID, msg); err != nil {
			s.log.Error("mark batch job failed", "job", job.ID, "err", err)
		}
		if err := s.store.UpdateProjectStatus(ctx, job.ProjectID, "error"); err != nil {
			s.log.Warn("mark project error", "job", job.ID, "err", err)
		}
		s.jobsTotal.WithLabelValues(job.Type, "failed").Inc()
	}
	var body batchRequestBody
	if err := json.Unmarshal([]byte(job.Payload), &body); err != nil {
		fail("invalid batch payload: " + err.Error())
		return
	}

	modelCtx := ctx
	privacyMode, modeErr := privacy.NormalizeMode(proj.PrivacyMode)
	if modeErr != nil {
		fail("invalid project privacy policy")
		return
	}
	if privacyMode == privacy.Edge {
		modelCtx = embed.WithForceLocal(ctx, true)
	}
	info, err := s.emb.ModelInfo(modelCtx, proj.Model)
	if err != nil {
		fail("model info: " + err.Error())
		return
	}
	if err := s.store.EnsureChunksTable(ctx, info.Dims); err != nil {
		fail("ensure chunks table: " + err.Error())
		return
	}

	indexed, chunks, deleted, errors := s.processBatchFiles(ctx, proj, body.Files, body.Delete, body.ProjectFiles, info.Dims)
	if err := s.store.CompleteJob(ctx, job.ID, indexed, chunks, deleted, errors); err != nil {
		s.log.Error("mark job complete", "job", job.ID, "err", err)
		fail("complete batch job: " + err.Error())
		return
	}
	s.jobsTotal.WithLabelValues(job.Type, "succeeded").Inc()
	s.log.Info("batch job done", "job", job.ID, "project", proj.Name,
		"files", indexed, "chunks", chunks, "deleted", deleted, "errors", errors)
}

func isForceJob(payload string) bool {
	if payload == "" {
		return false
	}
	var v struct {
		Force bool `json:"force"`
	}
	_ = json.Unmarshal([]byte(payload), &v)
	return v.Force
}

func (s *Server) handleEnqueueJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type  string `json:"type"`
		Force bool   `json:"force"`
	}
	// An empty body is allowed (defaults to a full index).
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Type == "" {
		body.Type = "full"
	}
	if body.Type != "full" && body.Type != "git_history" {
		writeJSONError(w, http.StatusBadRequest, "type must be 'full' or 'git_history'")
		return
	}

	proj, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, msgProjectNotFound)
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return
	}

	payload := ""
	if body.Force {
		payload = `{"force":true}`
	}
	id, err := s.store.EnqueueJobWithPayload(r.Context(), proj.ID, body.Type, payload)
	if err != nil {
		s.log.Error("enqueue job", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not enqueue job")
		return
	}
	s.jobsQueued.Inc()
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id, "status": "queued", "force": body.Force})
}

type jobView struct {
	ID            int    `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	FilesIndexed  int    `json:"files_indexed"`
	ChunksCreated int    `json:"chunks_created"`
	DeletedFiles  int    `json:"deleted_files"`
	ErrorCount    int    `json:"error_count"`
	ProgressDone  int    `json:"progress_done,omitempty"`
	ProgressTotal int    `json:"progress_total,omitempty"`
}

func jobViewFromStore(job *store.Job) jobView {
	v := jobView{
		ID: job.ID, Type: job.Type, Status: job.Status,
		FilesIndexed: job.FilesIndexed, ChunksCreated: job.ChunksCreated,
		DeletedFiles: job.DeletedFiles, ErrorCount: job.ErrorCount,
		ProgressDone: job.ProgressDone, ProgressTotal: job.ProgressTotal,
	}
	if job.Status == "failed" && job.Error != "" {
		v.Error = "index job failed"
	}
	return v
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	if project == "" {
		writeJSONError(w, http.StatusBadRequest, "project query parameter is required")
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	s.writeJobForProject(w, r, project, id)
}

func (s *Server) handleGetProjectJob(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	s.writeJobForProject(w, r, project, id)
}

func (s *Server) writeJobForProject(w http.ResponseWriter, r *http.Request, project string, id int) {
	job, err := s.store.GetJob(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, errJobNotFound)
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load job")
		return
	}
	proj, err := s.store.GetProjectByID(r.Context(), job.ProjectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, errJobNotFound)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "could not load job")
		return
	}
	if proj.Name != project {
		writeJSONError(w, http.StatusNotFound, errJobNotFound)
		return
	}
	writeJSON(w, http.StatusOK, jobViewFromStore(job))
}
