package client

import (
	"context"
	"net/http"
	"strconv"
)

// GitCredential is the public API view of a stored git credential (no secret).
type GitCredential struct {
	ID             int    `json:"id"`
	Scope          string `json:"scope"`
	ProjectID      *int   `json:"project_id,omitempty"`
	ProjectName    string `json:"project_name,omitempty"`
	Host           string `json:"host,omitempty"`
	Kind           string `json:"kind"`
	Username       string `json:"username"`
	Label          string `json:"label"`
	SSHKnownHosts  string `json:"ssh_known_hosts,omitempty"`
	SSHFingerprint string `json:"ssh_fingerprint,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// ProjectCredentialInput is the optional inline credential on project create.
type ProjectCredentialInput struct {
	Kind          string
	Username      string
	Secret        string
	Label         string
	SSHKnownHosts string
}

// CreateProjectParams carries the knobs for CreateProjectWithParams.
type CreateProjectParams struct {
	Name, Model, SourceType, GitURL, Branch string
	Credential                              *ProjectCredentialInput
}

// CreateProjectWithParams registers a project, optionally with an inline git
// credential (admin scope required on the server when Credential is set).
func (c *Client) CreateProjectWithParams(ctx context.Context, p CreateProjectParams) (*Project, error) {
	body := map[string]any{
		"name":  p.Name,
		"model": p.Model,
		"source": map[string]string{
			"type": p.SourceType, "url": p.GitURL, "branch": p.Branch,
		},
	}
	if p.Credential != nil && p.Credential.Secret != "" {
		body["credential"] = map[string]string{
			"kind":            p.Credential.Kind,
			"username":        p.Credential.Username,
			"secret":          p.Credential.Secret,
			"label":           p.Credential.Label,
			"ssh_known_hosts": p.Credential.SSHKnownHosts,
		}
	}
	var out Project
	if err := c.do(ctx, http.MethodPost, "/api/v1/projects", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GitCredentialInput creates a host- or project-scoped credential.
type GitCredentialInput struct {
	ProjectID     *int
	Host          string
	Kind          string
	Username      string
	Secret        string
	Label         string
	SSHKnownHosts string
}

// GitCredentialUpdateInput replaces mutable fields. An empty Secret leaves the
// stored secret unchanged.
type GitCredentialUpdateInput struct {
	Kind          string
	Username      string
	Secret        string
	Label         string
	SSHKnownHosts string
}

// ListGitCredentials returns every stored git credential (admin scope).
func (c *Client) ListGitCredentials(ctx context.Context) ([]GitCredential, error) {
	var out struct {
		Credentials []GitCredential `json:"credentials"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/git-credentials", nil, &out); err != nil {
		return nil, err
	}
	return out.Credentials, nil
}

// CreateGitCredential stores a new host- or project-scoped credential.
func (c *Client) CreateGitCredential(ctx context.Context, in GitCredentialInput) (*GitCredential, error) {
	body := map[string]any{
		"kind": in.Kind, "username": in.Username, "secret": in.Secret,
		"label": in.Label, "ssh_known_hosts": in.SSHKnownHosts,
	}
	if in.ProjectID != nil {
		body["project_id"] = *in.ProjectID
	}
	if in.Host != "" {
		body["host"] = in.Host
	}
	var out GitCredential
	if err := c.do(ctx, http.MethodPost, "/api/v1/git-credentials", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateGitCredential replaces mutable fields of credential id.
func (c *Client) UpdateGitCredential(ctx context.Context, id int, in GitCredentialUpdateInput) (*GitCredential, error) {
	body := map[string]string{
		"kind": in.Kind, "username": in.Username, "secret": in.Secret,
		"label": in.Label, "ssh_known_hosts": in.SSHKnownHosts,
	}
	var out GitCredential
	path := "/api/v1/git-credentials/" + strconv.Itoa(id)
	if err := c.do(ctx, http.MethodPut, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteGitCredential removes a credential by id.
func (c *Client) DeleteGitCredential(ctx context.Context, id int) error {
	path := "/api/v1/git-credentials/" + strconv.Itoa(id)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
