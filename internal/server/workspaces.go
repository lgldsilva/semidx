package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/lgldsilva/semidx/internal/store"
)

var validWorkspaceSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

type workspaceView struct {
	ID       int    `json:"id"`
	TenantID int    `json:"tenant_id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
}

func toWorkspaceView(w store.Workspace) workspaceView {
	return workspaceView{ID: w.ID, TenantID: w.TenantID, Slug: w.Slug, Name: w.Name}
}

func workspaceStore(s *Server) (store.WorkspaceStore, bool) {
	ws, ok := s.store.(store.WorkspaceStore)
	return ws, ok
}

func (s *Server) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	ws, ok := workspaceStore(s)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "workspace management requires PostgreSQL")
		return
	}
	items, err := ws.ListWorkspaces(r.Context())
	if err != nil {
		s.log.Error("list workspaces", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not list workspaces")
		return
	}
	out := make([]workspaceView, 0, len(items))
	for _, item := range items {
		out = append(out, toWorkspaceView(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": out})
}

func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, ok := workspaceStore(s)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "workspace management requires PostgreSQL")
		return
	}
	var body struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Slug = strings.ToLower(strings.TrimSpace(body.Slug))
	body.Name = strings.TrimSpace(body.Name)
	if !validWorkspaceSlug.MatchString(body.Slug) {
		writeJSONError(w, http.StatusBadRequest, "slug must contain 2-63 lowercase letters, digits, or hyphens")
		return
	}
	if body.Name == "" || len(body.Name) > 255 {
		writeJSONError(w, http.StatusBadRequest, "name is required and must be at most 255 characters")
		return
	}
	item, err := ws.CreateWorkspace(r.Context(), body.Slug, body.Name)
	switch {
	case errors.Is(err, store.ErrWorkspaceExists):
		writeJSONError(w, http.StatusConflict, "workspace already exists")
	case err != nil:
		s.log.Error("create workspace", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not create workspace")
	default:
		writeJSON(w, http.StatusCreated, toWorkspaceView(*item))
	}
}
