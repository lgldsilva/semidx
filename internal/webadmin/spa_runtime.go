package webadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lgldsilva/semidx/internal/privacy"
	"github.com/lgldsilva/semidx/internal/store"
)

func (a *Admin) apiProjectRuntimeEdges(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	graph, ok := a.store.(store.RuntimeGraphStore)
	if !ok {
		writeJSONErr(w, http.StatusNotImplemented, "runtime graph is unavailable")
		return
	}
	project, err := a.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	edges, err := graph.ListRuntimeEdges(r.Context(), project.ID)
	if err != nil {
		a.log.Error("list runtime edges", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Name, "edges": edges})
}

func (a *Admin) apiUsage(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	qs, ok := a.store.(store.QuotaStore)
	if !ok {
		writeJSONErr(w, http.StatusNotImplemented, "tenant usage is unavailable")
		return
	}
	quota, err := qs.GetTenantQuota(r.Context())
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	usage, err := qs.GetTenantUsage(r.Context())
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quota": quota, "usage": usage})
}

func (a *Admin) apiRuntimeGraph(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	graph, ok := a.store.(store.RuntimeGraphStore)
	if !ok {
		writeJSONErr(w, http.StatusNotImplemented, "runtime graph is unavailable")
		return
	}
	edges, err := graph.ListWorkspaceRuntimeEdges(r.Context(), 500)
	if err != nil {
		a.log.Error("list runtime graph", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": edges})
}

func (a *Admin) apiProjectPrivacy(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	policy, ok := a.store.(store.ProjectPolicyStore)
	if !ok {
		writeJSONErr(w, http.StatusNotImplemented, "project privacy policies are unavailable")
		return
	}
	project, err := a.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONErr(w, http.StatusBadRequest, spaErrInvalidJSONBody)
		return
	}
	mode, err := privacy.NormalizeMode(strings.TrimSpace(body.Mode))
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := policy.SetProjectPrivacy(r.Context(), project.ID, string(mode)); err != nil {
		a.log.Error("set project privacy", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	project.PrivacyMode = string(mode)
	writeJSON(w, http.StatusOK, projectToItem(*project))
}
