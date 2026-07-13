package webadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lgldsilva/semidx/internal/sbom"
	"github.com/lgldsilva/semidx/internal/store"
)

// apiProjectSbom returns an SBOM summary plus the parsed document for the admin UI.
// Query: format=cyclonedx-json|spdx-json (default cyclonedx-json).
func (a *Admin) apiProjectSbom(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	format := strings.TrimSpace(r.URL.Query().Get("format"))
	if format == "" {
		format = "cyclonedx-json"
	}
	proj, err := a.store.GetProject(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	doc, err := sbom.Generate(r.Context(), a.store, proj, format, "semidx-admin")
	if err != nil {
		a.log.Error("sbom generate failed", "project", name, "err", err)
		writeJSONErr(w, http.StatusBadGateway, "SBOM generation failed")
		return
	}
	count, err := sbom.ComponentCount(r.Context(), a.store, proj)
	if err != nil {
		a.log.Error("sbom component count failed", "project", name, "err", err)
		writeJSONErr(w, http.StatusBadGateway, "SBOM generation failed")
		return
	}
	var parsed any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		a.log.Error("sbom json parse failed", "project", name, "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"format":          format,
		"component_count": count,
		"document":        parsed,
		"cli_equivalent":  "semidx sbom generate --project " + proj.Name,
	})
}
