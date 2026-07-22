package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lgldsilva/semidx/internal/depcatalog"
	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/pkg/client"
)

func (s *Server) handleListDependencies(w http.ResponseWriter, r *http.Request) {
	depStore, ok := s.store.(store.DependencyStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "dependency catalog is unavailable")
		return
	}
	project, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	deps, err := depStore.ListProjectDependencies(r.Context(), project.ID)
	if err != nil {
		s.log.Error("list project dependencies", "project", project.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not load dependencies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Name, "dependencies": deps})
}

func (s *Server) handleSharedDependencies(w http.ResponseWriter, r *http.Request) {
	depStore, ok := s.store.(store.DependencyStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "dependency catalog is unavailable")
		return
	}
	project, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	deps, err := depStore.FindProjectsSharingDependency(r.Context(), project.ID)
	if err != nil {
		s.log.Error("find shared dependencies", "project", project.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not compare dependencies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Name, "dependencies": deps})
}

// handleResolveDependencies starts a managed-worker resolution or returns the
// customer-agent contract. Agent mode never executes package-manager commands
// on the server; the agent submits its result to the companion endpoint below.
func (s *Server) handleResolveDependencies(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	mode := strings.ToLower(strings.TrimSpace(body.Mode))
	if mode == "" {
		mode = "managed"
	}
	if mode != "managed" && mode != "agent" {
		writeJSONError(w, http.StatusBadRequest, "mode must be managed or agent")
		return
	}
	project, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	if mode == "agent" {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"project": project.Name, "mode": "agent", "status": "awaiting_agent",
			"submit": "/api/v1/projects/" + project.Name + "/dependencies/submit",
		})
		return
	}
	id, err := s.store.EnqueueJob(r.Context(), project.ID, "resolve_dependencies")
	if err != nil {
		s.log.Error("enqueue dependency resolution", "project", project.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not enqueue dependency resolution")
		return
	}
	s.jobsQueued.Inc()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"project": project.Name, "mode": "managed", "status": "queued", "job_id": id,
	})
}

// handleSubmitDependencies is the small, stateless customer-agent protocol:
// run native tools next to the source, then send the normalized result. The
// server replaces the project's catalog atomically and never receives source
// files or package-manager credentials through this endpoint.
func (s *Server) handleSubmitDependencies(w http.ResponseWriter, r *http.Request) {
	depStore, ok := s.store.(store.DependencyStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "dependency catalog is unavailable")
		return
	}
	project, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	var body struct {
		Dependencies []client.Dependency `json:"dependencies"`
		Source       string              `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Dependencies) > 10000 {
		writeJSONError(w, http.StatusBadRequest, "too many dependencies")
		return
	}
	deps := make([]store.Dependency, 0, len(body.Dependencies))
	for _, item := range body.Dependencies {
		ecosystem := depcatalog.Ecosystem(strings.ToLower(strings.TrimSpace(item.Ecosystem)))
		name := strings.TrimSpace(item.Name)
		if !depcatalog.ValidEcosystem(ecosystem) || name == "" {
			writeJSONError(w, http.StatusBadRequest, "each dependency needs a supported ecosystem and name")
			return
		}
		normalized := depcatalog.NormalizeName(ecosystem, name)
		if normalized == "" {
			writeJSONError(w, http.StatusBadRequest, "dependency name is invalid")
			return
		}
		source := strings.TrimSpace(item.Source)
		if source == "" {
			source = strings.TrimSpace(body.Source)
		}
		if source == "" {
			source = "customer-agent"
		}
		deps = append(deps, store.Dependency{
			Ecosystem: string(ecosystem), Name: name, NormalizedName: normalized,
			Constraint: item.Constraint, ResolvedVersion: item.ResolvedVersion,
			Scope: item.Scope, Source: source, Manifest: item.Manifest, Direct: item.Direct,
		})
	}
	if err := depStore.ReplaceProjectDependencies(r.Context(), project.ID, deps); err != nil {
		s.log.Error("submit dependencies", "project", project.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not store dependencies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Name, "status": "ready", "count": len(deps)})
}
