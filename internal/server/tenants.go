package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/lgldsilva/semidx/internal/store"
)

var validTenantSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

type tenantView struct {
	ID   int    `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

func toTenantView(t store.Tenant) tenantView {
	return tenantView{ID: t.ID, Slug: t.Slug, Name: t.Name}
}

func tenantStore(s *Server) (store.TenantStore, bool) {
	ts, ok := s.store.(store.TenantStore)
	return ts, ok
}

func (s *Server) handleListTenants(w http.ResponseWriter, r *http.Request) {
	ts, ok := tenantStore(s)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "tenant management requires PostgreSQL")
		return
	}
	tenants, err := ts.ListTenants(r.Context())
	if err != nil {
		s.log.Error("list tenants", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not list tenants")
		return
	}
	views := make([]tenantView, 0, len(tenants))
	for _, t := range tenants {
		views = append(views, toTenantView(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": views})
}

func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	ts, ok := tenantStore(s)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "tenant management requires PostgreSQL")
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
	if !validTenantSlug.MatchString(body.Slug) {
		writeJSONError(w, http.StatusBadRequest, "slug must contain 2-63 lowercase letters, digits, or hyphens")
		return
	}
	if body.Name == "" || len(body.Name) > 255 {
		writeJSONError(w, http.StatusBadRequest, "name is required and must be at most 255 characters")
		return
	}
	t, err := ts.CreateTenant(r.Context(), body.Slug, body.Name)
	switch {
	case errors.Is(err, store.ErrTenantExists):
		writeJSONError(w, http.StatusConflict, "tenant already exists")
	case err != nil:
		s.log.Error("create tenant", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not create tenant")
	default:
		writeJSON(w, http.StatusCreated, toTenantView(*t))
	}
}
