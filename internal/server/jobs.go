package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/lgldsilva/semidx/internal/gitsync"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

// StartWorkers launches n background workers that drain queued index jobs until
// ctx is cancelled. Git projects are cloned/pulled into dataDir first.
func (s *Server) StartWorkers(ctx context.Context, n int, dataDir string) {
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		go s.worker(ctx, dataDir)
	}
	s.log.Info("job workers started", "count", n)
}

func (s *Server) worker(ctx context.Context, dataDir string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Drain all currently-queued jobs before sleeping again.
			for s.claimAndRun(ctx, dataDir) {
				if ctx.Err() != nil {
					return
				}
			}
		}
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
	s.runJob(ctx, job, dataDir)
	return true
}

func (s *Server) runJob(ctx context.Context, job *store.Job, dataDir string) {
	fail := func(msg string) {
		s.log.Error("index job failed", "job", job.ID, "err", msg)
		if err := s.store.FailJob(ctx, job.ID, msg); err != nil {
			s.log.Error("mark job failed", "job", job.ID, "err", err)
		}
	}

	proj, err := s.store.GetProjectByID(ctx, job.ProjectID)
	if err != nil {
		fail("project not found: " + err.Error())
		return
	}

	path := proj.Path
	if proj.SourceType == "git" {
		p, err := gitsync.Sync(ctx, dataDir, proj.Name, proj.GitURL, proj.Branch)
		if err != nil {
			fail(err.Error())
			return
		}
		path = p
	}
	if path == "" {
		fail("project has no indexable source path (push projects upload via files/batch)")
		return
	}

	info, err := s.emb.ModelInfo(ctx, proj.Model)
	if err != nil {
		fail("model info: " + err.Error())
		return
	}
	if err := s.store.EnsureChunksTable(ctx, info.Dims); err != nil {
		fail("ensure chunks table: " + err.Error())
		return
	}

	idx := indexing.NewIndexer(s.store, s.emb, info.Dims, 0, 0, 0, 0, false, job.Type == "git_history", "30.days", nil)
	stats, err := idx.IndexProject(ctx, job.ProjectID, path, proj.Model, 0)
	if err != nil {
		fail("index: " + err.Error())
		return
	}
	if err := s.store.CompleteJob(ctx, job.ID, stats.FilesIndexed, stats.ChunksCreated); err != nil {
		s.log.Error("mark job complete", "job", job.ID, "err", err)
	}
	s.log.Info("index job done", "job", job.ID, "project", proj.Name,
		"files", stats.FilesIndexed, "chunks", stats.ChunksCreated)
}

func (s *Server) handleEnqueueJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type string `json:"type"`
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
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return
	}

	id, err := s.store.EnqueueJob(r.Context(), proj.ID, body.Type)
	if err != nil {
		s.log.Error("enqueue job", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not enqueue job")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id, "status": "queued"})
}

type jobView struct {
	ID            int    `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	FilesIndexed  int    `json:"files_indexed"`
	ChunksCreated int    `json:"chunks_created"`
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	job, err := s.store.GetJob(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load job")
		return
	}
	writeJSON(w, http.StatusOK, jobView{
		ID: job.ID, Type: job.Type, Status: job.Status, Error: job.Error,
		FilesIndexed: job.FilesIndexed, ChunksCreated: job.ChunksCreated,
	})
}
