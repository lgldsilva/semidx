package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lgldsilva/semidx/internal/store"
)

const maxRuntimeEdgesPerRequest = 1000

type runtimeEdgeInput struct {
	TargetProject   string  `json:"target_project"`
	SourceComponent string  `json:"source_component"`
	TargetComponent string  `json:"target_component"`
	Protocol        string  `json:"protocol"`
	Environment     string  `json:"environment"`
	RequestCount    int64   `json:"request_count"`
	ErrorCount      int64   `json:"error_count"`
	P95LatencyMS    float64 `json:"p95_latency_ms"`
}

// handleListRuntimeEdges returns observed calls made by one project. Runtime
// evidence is deliberately separate from file_dependencies: a deployment can
// communicate with another project or an external service without a source
// import edge.
func (s *Server) handleListRuntimeEdges(w http.ResponseWriter, r *http.Request) {
	graph, ok := s.store.(store.RuntimeGraphStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "runtime graph is unavailable")
		return
	}
	project, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	edges, err := graph.ListRuntimeEdges(r.Context(), project.ID)
	if err != nil {
		s.log.Error("list runtime edges", "project", project.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not load runtime graph")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project.Name, "edges": edges})
}

func (s *Server) handleListWorkspaceRuntimeEdges(w http.ResponseWriter, r *http.Request) {
	graph, ok := s.store.(store.RuntimeGraphStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "runtime graph is unavailable")
		return
	}
	limit := 500
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if _, err := fmt.Sscan(raw, &limit); err != nil || limit < 1 {
			writeJSONError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
	}
	if limit > 5000 {
		limit = 5000
	}
	edges, err := graph.ListWorkspaceRuntimeEdges(r.Context(), limit)
	if err != nil {
		s.log.Error("list workspace runtime edges", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not load runtime graph")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": edges, "limit": limit})
}

func (s *Server) handleSubmitRuntimeEdges(w http.ResponseWriter, r *http.Request) {
	graph, ok := s.store.(store.RuntimeGraphStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "runtime graph is unavailable")
		return
	}
	var body struct {
		Edges []runtimeEdgeInput `json:"edges"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Edges) == 0 {
		writeJSONError(w, http.StatusBadRequest, "edges are required")
		return
	}
	if len(body.Edges) > maxRuntimeEdgesPerRequest {
		writeJSONError(w, http.StatusBadRequest, "too many runtime edges")
		return
	}
	if err := s.enforceRuntimeEdgeQuota(r.Context(), len(body.Edges)); err != nil {
		if errors.Is(err, errQuotaExceeded) {
			writeJSONError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		s.log.Error("runtime edge quota lookup", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not evaluate tenant quota")
		return
	}
	project, err := s.store.GetProject(r.Context(), r.PathValue("project"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "could not load project")
		return
	}
	edges := make([]store.RuntimeEdge, 0, len(body.Edges))
	for _, input := range body.Edges {
		target := strings.TrimSpace(input.TargetProject)
		if target == "" {
			writeJSONError(w, http.StatusBadRequest, "target_project is required")
			return
		}
		targetID := 0
		if targetProject, targetErr := s.store.GetProject(r.Context(), target); targetErr == nil {
			targetID = targetProject.ID
		} else if !errors.Is(targetErr, store.ErrNotFound) {
			writeJSONError(w, http.StatusInternalServerError, "could not resolve target project")
			return
		}
		edges = append(edges, store.RuntimeEdge{
			TargetProjectID: targetID, TargetProjectName: target,
			SourceComponent: input.SourceComponent, TargetComponent: input.TargetComponent,
			Protocol: input.Protocol, Environment: input.Environment,
			RequestCount: input.RequestCount, ErrorCount: input.ErrorCount,
			P95LatencyMS: input.P95LatencyMS,
		})
	}
	if err := graph.UpsertRuntimeEdges(r.Context(), project.ID, edges); err != nil {
		s.log.Error("store runtime edges", "project", project.Name, "err", err)
		writeJSONError(w, http.StatusBadRequest, "could not store runtime graph")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"project": project.Name, "accepted": len(edges)})
}
