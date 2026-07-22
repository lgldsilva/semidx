package webadmin

import (
	"errors"
	"net/http"

	"github.com/lgldsilva/semidx/internal/store"
)

type dependencyItem struct {
	Ecosystem       string `json:"ecosystem"`
	Name            string `json:"name"`
	NormalizedName  string `json:"normalized_name"`
	Constraint      string `json:"constraint,omitempty"`
	ResolvedVersion string `json:"resolved_version,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Source          string `json:"source,omitempty"`
	Manifest        string `json:"manifest"`
	Direct          bool   `json:"direct"`
}

type dependencyUsageItem struct {
	ProjectID       int    `json:"project_id"`
	ProjectName     string `json:"project_name"`
	Ecosystem       string `json:"ecosystem"`
	Name            string `json:"name"`
	NormalizedName  string `json:"normalized_name"`
	Constraint      string `json:"constraint,omitempty"`
	ResolvedVersion string `json:"resolved_version,omitempty"`
	Scope           string `json:"scope,omitempty"`
	Direct          bool   `json:"direct"`
}

func dependencyItemFromStore(dep store.Dependency) dependencyItem {
	return dependencyItem{Ecosystem: dep.Ecosystem, Name: dep.Name, NormalizedName: dep.NormalizedName, Constraint: dep.Constraint, ResolvedVersion: dep.ResolvedVersion, Scope: dep.Scope, Source: dep.Source, Manifest: dep.Manifest, Direct: dep.Direct}
}

func (a *Admin) apiProjectDependencies(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	depStore, ok := a.store.(store.DependencyStore)
	if !ok {
		writeJSONErr(w, http.StatusNotImplemented, "dependency catalog is unavailable")
		return
	}
	project, err := a.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		a.log.Error("load dependency project", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	deps, err := depStore.ListProjectDependencies(r.Context(), project.ID)
	if err != nil {
		a.log.Error("list project dependencies", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	out := make([]dependencyItem, 0, len(deps))
	for _, dep := range deps {
		out = append(out, dependencyItemFromStore(dep))
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Name, "dependencies": out})
}

func (a *Admin) apiProjectSharedDependencies(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	depStore, ok := a.store.(store.DependencyStore)
	if !ok {
		writeJSONErr(w, http.StatusNotImplemented, "dependency catalog is unavailable")
		return
	}
	project, err := a.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		a.log.Error("load shared dependency project", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	deps, err := depStore.FindProjectsSharingDependency(r.Context(), project.ID)
	if err != nil {
		a.log.Error("find shared dependencies", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	out := make([]dependencyUsageItem, 0, len(deps))
	for _, dep := range deps {
		out = append(out, dependencyUsageItem{ProjectID: dep.ProjectID, ProjectName: dep.ProjectName, Ecosystem: dep.Ecosystem, Name: dep.Name, NormalizedName: dep.NormalizedName, Constraint: dep.Constraint, ResolvedVersion: dep.ResolvedVersion, Scope: dep.Scope, Direct: dep.Direct})
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Name, "dependencies": out})
}
