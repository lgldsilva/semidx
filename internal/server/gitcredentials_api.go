package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitcredmgr"
	"github.com/lgldsilva/semidx/internal/store"
)

func (s *Server) credMgr() *gitcredmgr.Service {
	return gitcredmgr.New(s.store, s.secrets, s.log)
}

func (s *Server) handleListGitCredentials(w http.ResponseWriter, r *http.Request) {
	creds, err := s.credMgr().List(r.Context())
	if err != nil {
		writeGitCredAPIError(w, s.log, "list git credentials", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": creds})
}

func (s *Server) handleCreateGitCredential(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID     *int   `json:"project_id"`
		Host          string `json:"host"`
		Kind          string `json:"kind"`
		Username      string `json:"username"`
		Secret        string `json:"secret"`
		Label         string `json:"label"`
		SSHKnownHosts string `json:"ssh_known_hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Host) != "" && body.ProjectID != nil {
		writeJSONError(w, http.StatusBadRequest, "set exactly one of project_id or host")
		return
	}
	created, err := s.credMgr().Create(r.Context(), gitcredmgr.CreateInput{
		ProjectID: body.ProjectID, Host: body.Host, Kind: body.Kind,
		Username: body.Username, Secret: body.Secret, Label: body.Label,
		SSHKnownHosts: body.SSHKnownHosts,
	})
	if err != nil {
		writeGitCredAPIError(w, s.log, "create git credential", err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateGitCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	var body struct {
		Kind          string `json:"kind"`
		Username      string `json:"username"`
		Secret        string `json:"secret"`
		Label         string `json:"label"`
		SSHKnownHosts string `json:"ssh_known_hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	updated, err := s.credMgr().Update(r.Context(), id, gitcredmgr.UpdateInput{
		Kind: body.Kind, Username: body.Username, Secret: body.Secret,
		Label: body.Label, SSHKnownHosts: body.SSHKnownHosts,
	})
	if err != nil {
		writeGitCredAPIError(w, s.log, "update git credential", err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteGitCredential(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	if err := s.credMgr().Delete(r.Context(), id); err != nil {
		writeGitCredAPIError(w, s.log, "delete git credential", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeGitCredAPIError(w http.ResponseWriter, log interface {
	Error(msg string, args ...any)
}, action string, err error) {
	switch {
	case errors.Is(err, gitcredmgr.ErrUnsupported):
		writeJSONError(w, http.StatusNotImplemented, "git credentials require PostgreSQL")
	case errors.Is(err, gitcredmgr.ErrSecretboxDisabled):
		writeJSONError(w, http.StatusServiceUnavailable, "SEMIDX_SECRET_KEY is not set")
	case errors.Is(err, store.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "credential not found")
	case strings.Contains(err.Error(), "already exists"):
		writeJSONError(w, http.StatusConflict, err.Error())
	case strings.Contains(err.Error(), "passphrase-protected"),
		strings.Contains(err.Error(), "invalid SSH"),
		strings.Contains(err.Error(), "kind must"),
		strings.Contains(err.Error(), "secret is required"),
		strings.Contains(err.Error(), "set exactly one"):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		log.Error(action, "err", err)
		writeJSONError(w, http.StatusInternalServerError, "git credential operation failed")
	}
}

// projectCredentialBody is the optional inline credential on project create.
type projectCredentialBody struct {
	Kind          string `json:"kind"`
	Username      string `json:"username"`
	Secret        string `json:"secret"`
	Label         string `json:"label"`
	SSHKnownHosts string `json:"ssh_known_hosts"`
}

func (s *Server) createProjectCredential(ctx context.Context, projectID int, body *projectCredentialBody) error {
	if body == nil || strings.TrimSpace(body.Secret) == "" {
		return nil
	}
	_, err := s.credMgr().CreateForProject(ctx, projectID, gitcredmgr.CreateInput{
		Kind: body.Kind, Username: body.Username, Secret: body.Secret,
		Label: body.Label, SSHKnownHosts: body.SSHKnownHosts,
	})
	return err
}
