package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitmeta"
	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/store"
)

// validProjectName allows letters, digits, hyphens, underscores, and dots,
// must start with alphanumeric, and is at most 255 characters.
var validProjectName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`)

func validateProjectName(name string) error {
	if name == "" {
		return errors.New("project name is required")
	}
	if len(name) > 255 {
		return errors.New("project name must be at most 255 characters")
	}
	if !validProjectName.MatchString(name) {
		return errors.New("project name must start with alphanumeric and contain only letters, digits, hyphens, underscores, and dots")
	}
	return nil
}

type projectView struct {
	TenantID    int    `json:"tenant_id"`
	WorkspaceID int    `json:"workspace_id"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	Status      string `json:"status"`
	SourceType  string `json:"source_type"`
	GitURL      string `json:"git_url,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Identity    string `json:"identity,omitempty"`
	Path        string `json:"path,omitempty"`
	PrivacyMode string `json:"privacy_mode"`
}

func toProjectView(p *store.Project) projectView {
	return projectView{
		TenantID:    p.TenantID,
		WorkspaceID: p.WorkspaceID,
		Name:        p.Name, Model: p.Model, Status: p.Status,
		SourceType: p.SourceType, GitURL: gitmeta.RedactURL(p.GitURL), Branch: p.Branch,
		Identity: p.Identity, Path: p.Path, PrivacyMode: projectPrivacyMode(p.PrivacyMode),
	}
}

func projectPrivacyMode(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return string(privacy.Hybrid)
	}
	return mode
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Model       string `json:"model"`
		PrivacyMode string `json:"privacy_mode"`
		Source      struct {
			Type   string `json:"type"`
			URL    string `json:"url"`
			Branch string `json:"branch"`
		} `json:"source"`
		Credential *projectCredentialBody `json:"credential"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := validateProjectName(body.Name); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Model == "" {
		body.Model = "bge-m3"
	}
	if body.Source.Type == "" {
		body.Source.Type = "push"
	}
	privacyMode, err := privacy.NormalizeMode(body.PrivacyMode)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.enforceProjectQuota(r.Context()); err != nil {
		if errors.Is(err, errQuotaExceeded) {
			writeJSONError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		s.log.Error("project quota lookup", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not evaluate tenant quota")
		return
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
	if body.Credential != nil && strings.TrimSpace(body.Credential.Secret) != "" && body.Source.Type != "git" {
		writeJSONError(w, http.StatusBadRequest, "credential is only supported for git projects")
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
	if !s.attachCreateProjectCredential(w, r, p.ID, body.Credential) {
		return
	}
	if ps, ok := s.store.(store.ProjectPolicyStore); ok {
		if err := ps.SetProjectPrivacy(r.Context(), p.ID, string(privacyMode)); err != nil {
			s.log.Error("set project privacy", "err", err)
			writeJSONError(w, http.StatusInternalServerError, "could not save project privacy policy")
			return
		}
		p.PrivacyMode = string(privacyMode)
	}
	writeJSON(w, http.StatusCreated, toProjectView(p))
}

func (s *Server) attachCreateProjectCredential(w http.ResponseWriter, r *http.Request, projectID int, cred *projectCredentialBody) bool {
	if cred == nil || strings.TrimSpace(cred.Secret) == "" {
		return true
	}
	ok, err := s.bearerHasAdminScope(r)
	if err != nil {
		s.log.Error("credential scope check", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "auth check failed")
		return false
	}
	if !ok {
		writeJSONError(w, http.StatusForbidden, "admin scope required to set a git credential")
		return false
	}
	if err := s.createProjectCredential(r.Context(), projectID, cred); err != nil {
		writeGitCredAPIError(w, s.log, "create project credential", err)
		return false
	}
	return true
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseListParams(r)
	projects, err := s.store.ListProjects(r.Context(), limit, offset)
	if err != nil {
		s.log.Error("list projects", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not list projects")
		return
	}
	views := make([]projectView, 0, len(projects))
	for i := range projects {
		views = append(views, toProjectView(&projects[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": views, "limit": limit, "offset": offset})
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

func (s *Server) handleSetProjectPrivacy(w http.ResponseWriter, r *http.Request) {
	policy, ok := s.store.(store.ProjectPolicyStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "project privacy policies are unavailable")
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
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	mode, err := privacy.NormalizeMode(body.Mode)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := policy.SetProjectPrivacy(r.Context(), project.ID, string(mode)); err != nil {
		s.log.Error("set project privacy", "project", project.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not save project privacy policy")
		return
	}
	project.PrivacyMode = string(mode)
	writeJSON(w, http.StatusOK, toProjectView(project))
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
