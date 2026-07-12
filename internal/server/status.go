package server

import (
	"net/http"

	"github.com/lgldsilva/semidx/pkg/client"
)

// handleProjectStatus returns project metadata along with the total number of
// indexed files for the project.
func (s *Server) handleProjectStatus(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.loadProject(w, r)
	if !ok {
		return
	}

	count, err := s.store.CountProjectFiles(r.Context(), proj.ID)
	if err != nil {
		s.log.Error("count project files for status", "project", proj.Name, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not load project file info")
		return
	}

	writeJSON(w, http.StatusOK, client.StatusResponse{
		Name:       proj.Name,
		Identity:   proj.Identity,
		SourceType: proj.SourceType,
		Status:     proj.Status,
		Model:      proj.Model,
		TotalFiles: count,
	})
}
