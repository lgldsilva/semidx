package webadmin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/graph"
	"github.com/lgldsilva/semidx/internal/store"
)

// Query clamps mirroring the public API's, so the admin BFF cannot be used to
// ask for a bigger walk than a token holder could.
const (
	maxAdminPathDepth     = 16
	maxAdminSubgraphDepth = 5
	maxAdminSubgraphEdges = 2000
)

// apiProjectGraphSubgraph is the admin BFF for the file↔package subgraph
// contract: the same nodes/edges document the public API serves, so the SPA and
// an API client render identical graphs.
func (a *Admin) apiProjectGraphSubgraph(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	idx, ok := a.loadDepGraphIndex(w, r)
	if !ok {
		return
	}
	sg := idx.Subgraph(r.Context(), r.URL.Query().Get("seed"), graph.Budget{
		MaxDepth:    clampAdminQueryInt(r.URL.Query().Get("depth"), maxAdminSubgraphDepth),
		MaxEdgesOut: clampAdminQueryInt(r.URL.Query().Get("limit"), maxAdminSubgraphEdges),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": sg.Nodes, "edges": sg.Edges, "truncated": sg.Truncated,
	})
}

// apiProjectGraphPath is the admin BFF for shortest-path A→B.
func (a *Admin) apiProjectGraphPath(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	if from == "" || to == "" {
		writeJSONErr(w, http.StatusBadRequest, "from and to are required")
		return
	}
	idx, ok := a.loadDepGraphIndex(w, r)
	if !ok {
		return
	}
	undirected := r.URL.Query().Get("undirected") == "1" || r.URL.Query().Get("undirected") == "true"
	pr := idx.ShortestPath(r.Context(), from, to, graph.Budget{
		MaxDepth: clampAdminQueryInt(r.URL.Query().Get("max_depth"), maxAdminPathDepth),
	}, undirected)
	writeJSON(w, http.StatusOK, map[string]any{
		"from": pr.From, "to": pr.To, "found": pr.Found, "directed": pr.Directed,
		"hops": pr.Hops, "edges": pr.Edges, "length": pr.Length, "truncated": pr.Truncated,
	})
}

// loadDepGraphIndex resolves {project} and builds its walkable graph index,
// writing the sanitized error response itself and reporting ok=false.
func (a *Admin) loadDepGraphIndex(w http.ResponseWriter, r *http.Request) (*graph.Index, bool) {
	name := r.PathValue("project")
	proj, err := a.store.GetProject(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
			return nil, false
		}
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return nil, false
	}
	neighbors, err := a.store.FetchGraphNeighbors(r.Context(), proj.ID)
	if err != nil {
		a.log.Error("fetch graph failed", "project", name, "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgGraphLoadFailed)
		return nil, false
	}
	hashes, err := a.store.ListFileHashes(r.Context(), proj.ID)
	if err != nil {
		a.log.Error("list project files failed", "project", name, "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgGraphLoadFailed)
		return nil, false
	}
	files := make([]string, 0, len(hashes))
	for p := range hashes {
		files = append(files, p)
	}
	return graph.Build(neighbors, files), true
}

// clampAdminQueryInt parses a non-negative query int, capping it at max.
// An empty or malformed value yields 0, which asks graph for its default.
func clampAdminQueryInt(raw string, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	if n > max {
		return max
	}
	return n
}
