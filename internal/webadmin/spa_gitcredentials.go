package webadmin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/gitcredmgr"
	"github.com/lgldsilva/semidx/internal/secretbox"
	"github.com/lgldsilva/semidx/internal/store"
)

// SetSecretBox wires the AES vault used to seal git credentials from the admin API.
func (a *Admin) SetSecretBox(box *secretbox.Box) { a.secrets = box }

func (a *Admin) credMgr() *gitcredmgr.Service {
	return gitcredmgr.New(a.store, a.secrets, a.log)
}

func (a *Admin) apiListGitCredentials(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	creds, err := a.credMgr().List(r.Context())
	if err != nil {
		writeGitCredErr(w, a.log, "list git credentials", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": creds})
}

func (a *Admin) apiCreateGitCredential(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
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
		writeJSONErr(w, http.StatusBadRequest, spaErrInvalidJSON)
		return
	}
	if strings.TrimSpace(body.Host) != "" && body.ProjectID != nil {
		writeJSONErr(w, http.StatusBadRequest, "set exactly one of project_id or host")
		return
	}
	created, err := a.credMgr().Create(r.Context(), gitcredmgr.CreateInput{
		ProjectID: body.ProjectID, Host: body.Host, Kind: body.Kind,
		Username: body.Username, Secret: body.Secret, Label: body.Label,
		SSHKnownHosts: body.SSHKnownHosts,
	})
	if err != nil {
		writeGitCredErr(w, a.log, "create git credential", err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *Admin) apiUpdateGitCredential(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeJSONErr(w, http.StatusBadRequest, "invalid credential id")
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
		writeJSONErr(w, http.StatusBadRequest, spaErrInvalidJSON)
		return
	}
	updated, err := a.credMgr().Update(r.Context(), id, gitcredmgr.UpdateInput{
		Kind: body.Kind, Username: body.Username, Secret: body.Secret,
		Label: body.Label, SSHKnownHosts: body.SSHKnownHosts,
	})
	if err != nil {
		writeGitCredErr(w, a.log, "update git credential", err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *Admin) apiDeleteGitCredential(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeJSONErr(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	if err := a.credMgr().Delete(r.Context(), id); err != nil {
		writeGitCredErr(w, a.log, "delete git credential", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeGitCredErr(w http.ResponseWriter, log interface {
	Error(msg string, args ...any)
}, action string, err error) {
	switch {
	case errors.Is(err, gitcredmgr.ErrUnsupported):
		writeJSONErr(w, http.StatusNotImplemented, "git credentials require PostgreSQL")
	case errors.Is(err, gitcredmgr.ErrSecretboxDisabled):
		writeJSONErr(w, http.StatusServiceUnavailable, "SEMIDX_SECRET_KEY is not set")
	case errors.Is(err, store.ErrNotFound):
		writeJSONErr(w, http.StatusNotFound, "credential not found")
	case strings.Contains(err.Error(), "already exists"):
		writeJSONErr(w, http.StatusConflict, err.Error())
	case strings.Contains(err.Error(), "passphrase-protected"),
		strings.Contains(err.Error(), "invalid SSH"),
		strings.Contains(err.Error(), "kind must"),
		strings.Contains(err.Error(), "secret is required"),
		strings.Contains(err.Error(), "set exactly one"):
		writeJSONErr(w, http.StatusBadRequest, err.Error())
	default:
		log.Error(action, "err", err)
		writeJSONErr(w, http.StatusInternalServerError, "git credential operation failed")
	}
}

type inlineProjectCredential struct {
	Kind          string `json:"kind"`
	Username      string `json:"username"`
	Secret        string `json:"secret"`
	Label         string `json:"label"`
	SSHKnownHosts string `json:"ssh_known_hosts"`
}

func (a *Admin) createInlineProjectCredential(ctx context.Context, projectID int, body *inlineProjectCredential) error {
	if body == nil || strings.TrimSpace(body.Secret) == "" {
		return nil
	}
	_, err := a.credMgr().CreateForProject(ctx, projectID, gitcredmgr.CreateInput{
		Kind: body.Kind, Username: body.Username, Secret: body.Secret,
		Label: body.Label, SSHKnownHosts: body.SSHKnownHosts,
	})
	return err
}
