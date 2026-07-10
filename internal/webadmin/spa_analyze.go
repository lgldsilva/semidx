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

// msgGraphLoadFailed is the sanitized error returned when the dependency graph
// cannot be loaded (shared by the callers/deps/graph-stats handlers).
const msgGraphLoadFailed = "could not load dependency graph"

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
			writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	graph, err := a.store.FetchGraphNeighbors(r.Context(), proj.ID)
	if err != nil {
		a.log.Error("fetch graph failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgGraphLoadFailed)
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
			writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
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
		// Don't echo the raw analyzer error (go/packages output, server paths)
		// to the client — log it, return a safe message (REQ-SRCH-08 discipline).
		a.log.Error("deadcode analyze failed", "project", name, "err", err)
		writeJSONErr(w, http.StatusBadGateway, "dead-code analysis failed")
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
			writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	graph, err := a.store.FetchGraphNeighbors(r.Context(), proj.ID)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgGraphLoadFailed)
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

// degreeEntry is one node's connectivity in the dependency graph.
type degreeEntry struct {
	Node   string `json:"node"`
	Degree int    `json:"degree"`
}

// apiProjectGraphStats returns a progressive-disclosure summary of the project's
// dependency graph (REQ-UI-09): node/edge counts plus the most-connected nodes
// by out-degree (most dependencies) and in-degree (most depended-upon). This is
// the CSP-safe alternative to a node-link diagram — no external viz library.
func (a *Admin) apiProjectGraphStats(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	proj, err := a.store.GetProject(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
			return
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	graph, err := a.store.FetchGraphNeighbors(r.Context(), proj.ID)
	if err != nil {
		a.log.Error("fetch graph failed", "project", name, "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgGraphLoadFailed)
		return
	}

	limit := 10
	if n, _ := strconv.Atoi(r.URL.Query().Get("top")); n > 0 && n <= 100 {
		limit = n
	}

	outDeg := make(map[string]int, len(graph))
	inDeg := make(map[string]int)
	edges := 0
	for src, targets := range graph {
		outDeg[src] = len(targets)
		edges += len(targets)
		for _, t := range targets {
			inDeg[t]++
		}
	}
	// Union of all nodes (sources and pure targets) for an accurate node count.
	nodes := make(map[string]struct{}, len(outDeg))
	for n := range outDeg {
		nodes[n] = struct{}{}
	}
	for n := range inDeg {
		nodes[n] = struct{}{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":        len(nodes),
		"edges":        edges,
		"top_depends":  topDegrees(outDeg, limit),
		"top_depended": topDegrees(inDeg, limit),
	})
}

// topDegrees returns the highest-degree nodes, ties broken by node name so the
// output is deterministic.
func topDegrees(deg map[string]int, limit int) []degreeEntry {
	entries := make([]degreeEntry, 0, len(deg))
	for n, d := range deg {
		entries = append(entries, degreeEntry{Node: n, Degree: d})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Degree != entries[j].Degree {
			return entries[i].Degree > entries[j].Degree
		}
		return entries[i].Node < entries[j].Node
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}
