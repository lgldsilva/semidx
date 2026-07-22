package webadmin

import (
	"net/http"

	"github.com/lgldsilva/semidx/internal/store"
)

func (a *Admin) apiTenants(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	ts, ok := a.store.(store.TenantStore)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"tenants": []map[string]any{{"id": 1, "slug": "default", "name": "Default"}}})
		return
	}
	tenants, err := ts.ListTenants(r.Context())
	if err != nil {
		a.log.Error("list admin tenants", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	type view struct {
		ID   int    `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	out := make([]view, 0, len(tenants))
	for _, t := range tenants {
		out = append(out, view{ID: t.ID, Slug: t.Slug, Name: t.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": out})
}

func (a *Admin) apiWorkspaces(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	ws, ok := a.store.(store.WorkspaceStore)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"workspaces": []map[string]any{}})
		return
	}
	items, err := ws.ListWorkspaces(r.Context())
	if err != nil {
		a.log.Error("list admin workspaces", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	type view struct {
		ID       int    `json:"id"`
		TenantID int    `json:"tenant_id"`
		Slug     string `json:"slug"`
		Name     string `json:"name"`
	}
	out := make([]view, 0, len(items))
	for _, item := range items {
		out = append(out, view{ID: item.ID, TenantID: item.TenantID, Slug: item.Slug, Name: item.Name})
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": out})
}
