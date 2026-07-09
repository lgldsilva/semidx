package webadmin

import (
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/deadcode"
	"github.com/lgldsilva/semidx/internal/store"
)

// apiProjectCallers returns files that import the package directory of the given file.
// Query: path=internal/auth/token.go
func (a *Admin) apiProjectCallers(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		writeJSONErr(w, http.StatusBadRequest, "path is required")
		return
	}
	proj, err := a.store.GetProject(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	graph, err := a.store.FetchGraphNeighbors(r.Context(), proj.ID)
	if err != nil {
		a.log.Error("fetch graph failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "could not load dependency graph")
		return
	}
	fileDir := filepath.ToSlash(filepath.Dir(filePath))
	if fileDir != "." && !strings.HasSuffix(fileDir, "/") {
		fileDir += "/"
	}
	if fileDir == "./" {
		fileDir = ""
	}
	var direct []string
	for src, targets := range graph {
		for _, tgt := range targets {
			t := filepath.ToSlash(tgt)
			if t == fileDir || t == strings.TrimSuffix(fileDir, "/") ||
				strings.TrimSuffix(t, "/") == strings.TrimSuffix(fileDir, "/") {
				direct = append(direct, src)
				break
			}
		}
	}
	sort.Strings(direct)
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    filePath,
		"package": fileDir,
		"callers": direct,
		"count":   len(direct),
	})
}

// apiProjectDeadCode runs deadcode.Analyze when the project path is on disk.
func (a *Admin) apiProjectDeadCode(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	proj, err := a.store.GetProject(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	root := proj.Path
	if root == "" {
		writeJSONErr(w, http.StatusBadRequest, "project has no path on the server — dead-code needs a checkout (git projects or path-indexed docs)")
		return
	}
	findings, err := deadcode.Analyze(r.Context(), proj.ID, a.store, root)
	if err != nil {
		a.log.Error("deadcode analyze failed", "err", err)
		writeJSONErr(w, http.StatusBadGateway, err.Error())
		return
	}
	stats := deadcode.AggregateStats(findings)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	total := len(findings)
	if total > limit {
		findings = findings[:limit]
	}
	out := make([]map[string]any, 0, len(findings))
	for _, f := range findings {
		out = append(out, map[string]any{
			"symbol": f.Symbol, "kind": f.Kind, "file": f.File,
			"start_line": f.StartLine, "confidence": f.Confidence,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"findings": out,
		"stats": map[string]int{
			"total": stats.TotalFindings, "confirmed": stats.Confirmed, "public_api": stats.PublicAPI,
		},
		"truncated": len(out) < total,
	})
}

// apiProjectDeps returns outbound dependencies for a file from the graph.
func (a *Admin) apiProjectDeps(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if filePath == "" {
		writeJSONErr(w, http.StatusBadRequest, "path is required")
		return
	}
	proj, err := a.store.GetProject(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	graph, err := a.store.FetchGraphNeighbors(r.Context(), proj.ID)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "could not load dependency graph")
		return
	}
	deps := graph[filePath]
	if deps == nil {
		// try with/without leading clean
		for k, v := range graph {
			if filepath.ToSlash(k) == filepath.ToSlash(filePath) {
				deps = v
				break
			}
		}
	}
	if deps == nil {
		deps = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": filePath, "dependencies": deps, "count": len(deps),
	})
}
