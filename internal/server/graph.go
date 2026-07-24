package server

import (
	"net/http"
	"strconv"

	"github.com/lgldsilva/semidx/internal/graph"
	"github.com/lgldsilva/semidx/pkg/client"
)

const (
	maxPathDepthQuery     = 16
	maxSubgraphDepthQuery = 5
	maxSubgraphEdgesQuery = 2000
)

// handleGraphSubgraph returns a dependency neighborhood for a project.
// Query: seed (optional file), depth, limit (max edges).
func (s *Server) handleGraphSubgraph(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	idx, err := s.loadGraphIndex(r, proj.ID)
	if err != nil {
		s.log.Error("load dependency graph", "project", proj.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not load dependency graph")
		return
	}
	budget := graph.Budget{
		MaxDepth:    clampQueryInt(r.URL.Query().Get("depth"), 0, maxSubgraphDepthQuery),
		MaxEdgesOut: clampQueryInt(r.URL.Query().Get("limit"), 0, maxSubgraphEdgesQuery),
	}
	sg := idx.Subgraph(r.URL.Query().Get("seed"), budget)
	writeJSON(w, http.StatusOK, client.GraphSubgraphResponse{
		Nodes:     toClientNodes(sg.Nodes),
		Edges:     toClientEdges(sg.Edges),
		Truncated: sg.Truncated,
	})
}

// handleGraphPath returns the shortest dependency path between two files.
// Query: from, to (required); max_depth; undirected=1 to allow reverse hops.
func (s *Server) handleGraphPath(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProject(w, r)
	if !ok {
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		writeJSONError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	idx, err := s.loadGraphIndex(r, proj.ID)
	if err != nil {
		s.log.Error("load dependency graph", "project", proj.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not load dependency graph")
		return
	}
	budget := graph.Budget{
		MaxDepth: clampQueryInt(r.URL.Query().Get("max_depth"), 0, maxPathDepthQuery),
	}
	undirected := r.URL.Query().Get("undirected") == "1" ||
		r.URL.Query().Get("undirected") == "true"
	pr := idx.ShortestPath(from, to, budget, undirected)
	writeJSON(w, http.StatusOK, client.GraphPathResponse{
		From:      pr.From,
		To:        pr.To,
		Found:     pr.Found,
		Directed:  pr.Directed,
		Hops:      pr.Hops,
		Edges:     toClientEdges(pr.Edges),
		Length:    pr.Length,
		Truncated: pr.Truncated,
	})
}

func (s *Server) loadGraphIndex(r *http.Request, projectID int) (*graph.Index, error) {
	neighbors, err := s.store.FetchGraphNeighbors(r.Context(), projectID)
	if err != nil {
		return nil, err
	}
	hashes, err := s.store.ListFileHashes(r.Context(), projectID)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(hashes))
	for p := range hashes {
		files = append(files, p)
	}
	return graph.Build(neighbors, files), nil
}

func clampQueryInt(raw string, fallback, max int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fallback
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

func toClientNodes(in []graph.Node) []client.GraphNode {
	out := make([]client.GraphNode, len(in))
	for i, n := range in {
		out[i] = client.GraphNode{ID: n.ID, Label: n.Label, Kind: n.Kind, Seed: n.Seed}
	}
	return out
}

func toClientEdges(in []graph.Edge) []client.GraphEdge {
	out := make([]client.GraphEdge, len(in))
	for i, e := range in {
		out[i] = client.GraphEdge{Source: e.Source, Target: e.Target, Kind: e.Kind, Reverse: e.Reverse}
	}
	return out
}
