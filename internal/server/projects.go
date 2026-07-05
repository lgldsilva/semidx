package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lgldsilva/semidx/internal/store"
)

type projectView struct {
	Name       string `json:"name"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	SourceType string `json:"source_type"`
	GitURL     string `json:"git_url,omitempty"`
	Branch     string `json:"branch,omitempty"`
}

func toProjectView(p *store.Project) projectView {
	return projectView{
		Name: p.Name, Model: p.Model, Status: p.Status,
		SourceType: p.SourceType, GitURL: p.GitURL, Branch: p.Branch,
	}
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Model  string `json:"model"`
		Source struct {
			Type   string `json:"type"`
			URL    string `json:"url"`
			Branch string `json:"branch"`
		} `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.Model == "" {
		body.Model = "bge-m3"
	}
	if body.Source.Type == "" {
		body.Source.Type = "push"
	}
	switch body.Source.Type {
	case "push", "git":
	default:
		writeJSONError(w, http.StatusBadRequest, "source.type must be 'push' or 'git'")
		return
	}
	if body.Source.Type == "git" && body.Source.URL == "" {
		writeJSONError(w, http.StatusBadRequest, "source.url is required for git projects")
		return
	}

	p, err := s.store.CreateProject(r.Context(), body.Name, body.Model, body.Source.Type, body.Source.URL, body.Source.Branch, 0)
	switch {
	case errors.Is(err, store.ErrProjectExists):
		writeJSONError(w, http.StatusConflict, "project already exists: "+body.Name)
		return
	case err != nil:
		s.log.Error("create project", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not create project")
		return
	}
	writeJSON(w, http.StatusCreated, toProjectView(p))
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context(), 0, 0)
	if err != nil {
		s.log.Error("list projects", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not list projects")
		return
	}
	views := make([]projectView, 0, len(projects))
	for i := range projects {
		views = append(views, toProjectView(&projects[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": views})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	case err != nil:
		s.log.Error("get project", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not get project")
		return
	}
	writeJSON(w, http.StatusOK, toProjectView(p))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteProject(r.Context(), r.PathValue("project"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	case err != nil:
		s.log.Error("delete project", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not delete project")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
