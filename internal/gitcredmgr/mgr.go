// Package gitcredmgr implements CRUD for stored git credentials: validation,
// sealing via secretbox, and public views that never expose plaintext secrets.
package gitcredmgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/lgldsilva/semidx/internal/secretbox"
	"github.com/lgldsilva/semidx/internal/store"
)

// ErrUnsupported is returned when the backing store has no git credential support.
var ErrUnsupported = errors.New("git credentials require PostgreSQL")

// ErrSecretboxDisabled is returned when SEMIDX_SECRET_KEY is not configured.
var ErrSecretboxDisabled = errors.New("SEMIDX_SECRET_KEY is not set")

// PublicCredential is the API-safe view of a stored git credential.
type PublicCredential struct {
	ID             int    `json:"id"`
	Scope          string `json:"scope"` // "project" or "host"
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

// CreateInput carries plaintext secret material for a new credential.
type CreateInput struct {
	ProjectID     *int
	Host          string
	Kind          string
	Username      string
	Secret        string
	Label         string
	SSHKnownHosts string
}

// UpdateInput replaces mutable fields. An empty Secret leaves the stored secret unchanged.
type UpdateInput struct {
	Kind          string
	Username      string
	Secret        string
	Label         string
	SSHKnownHosts string
}

// Service manages git credentials against a GitCredentialStore.
type Service struct {
	store   store.Store
	secrets *secretbox.Box
	log     *slog.Logger
}

// New builds a credential manager. secrets may be nil (seal/open then fail clearly).
func New(st store.Store, secrets *secretbox.Box, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: st, secrets: secrets, log: log}
}

// Supported reports whether the store implements git credential persistence.
func (s *Service) Supported() bool {
	_, ok := s.store.(store.GitCredentialStore)
	return ok
}

func (s *Service) gcs() (store.GitCredentialStore, error) {
	gcs, ok := s.store.(store.GitCredentialStore)
	if !ok {
		return nil, ErrUnsupported
	}
	return gcs, nil
}

// List returns every credential as a public view.
func (s *Service) List(ctx context.Context) ([]PublicCredential, error) {
	gcs, err := s.gcs()
	if err != nil {
		return nil, err
	}
	creds, err := gcs.ListGitCredentials(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PublicCredential, 0, len(creds))
	for i := range creds {
		pub, err := s.toPublic(ctx, &creds[i])
		if err != nil {
			return nil, err
		}
		out = append(out, pub)
	}
	return out, nil
}

// Create seals and persists a new credential.
func (s *Service) Create(ctx context.Context, in CreateInput) (*PublicCredential, error) {
	gcs, err := s.gcs()
	if err != nil {
		return nil, err
	}
	if !s.secrets.Enabled() {
		return nil, ErrSecretboxDisabled
	}
	c, err := s.buildCredential(in)
	if err != nil {
		return nil, err
	}
	created, err := gcs.CreateGitCredential(ctx, c)
	if errors.Is(err, store.ErrCredentialExists) {
		return nil, fmt.Errorf("credential already exists for this scope")
	}
	if err != nil {
		return nil, err
	}
	pub, err := s.toPublic(ctx, created)
	if err != nil {
		return nil, err
	}
	return &pub, nil
}

// CreateForProject binds a credential to an existing project (used on project create).
func (s *Service) CreateForProject(ctx context.Context, projectID int, in CreateInput) (*PublicCredential, error) {
	in.ProjectID = &projectID
	in.Host = ""
	return s.Create(ctx, in)
}

// applyUpdateSecret resolves kind and optionally re-seals the secret for Update.
func (s *Service) applyUpdateSecret(existing *store.GitCredential, in UpdateInput) (kind string, secretEnc []byte, keyVersion int, err error) {
	kind = strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = existing.Kind
	} else if kind != "https" && kind != "ssh" {
		return "", nil, 0, errors.New("kind must be https or ssh")
	}
	secretEnc = existing.SecretEnc
	keyVersion = existing.KeyVersion
	secret := strings.TrimSpace(in.Secret)
	if secret != "" {
		if err := validateSecret(kind, secret); err != nil {
			return "", nil, 0, err
		}
		secretEnc, err = s.secrets.Seal([]byte(secret))
		if err != nil {
			return "", nil, 0, err
		}
		keyVersion = s.secrets.KeyVersion()
		return kind, secretEnc, keyVersion, nil
	}
	if kind != existing.Kind {
		return "", nil, 0, errors.New("secret is required when changing credential kind")
	}
	return kind, secretEnc, keyVersion, nil
}

// Update replaces mutable fields of the credential id.
func (s *Service) Update(ctx context.Context, id int, in UpdateInput) (*PublicCredential, error) {
	gcs, err := s.gcs()
	if err != nil {
		return nil, err
	}
	if !s.secrets.Enabled() {
		return nil, ErrSecretboxDisabled
	}
	existing, err := gcs.GetGitCredentialByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	kind, secretEnc, keyVersion, err := s.applyUpdateSecret(existing, in)
	if err != nil {
		return nil, err
	}
	username := strings.TrimSpace(in.Username)
	label := strings.TrimSpace(in.Label)
	knownHosts := strings.TrimSpace(in.SSHKnownHosts)
	updated := &store.GitCredential{
		ID: id, Kind: kind, Username: username, SecretEnc: secretEnc,
		KeyVersion: keyVersion, SSHKnownHosts: knownHosts, Label: label,
	}
	if err := gcs.UpdateGitCredential(ctx, updated); err != nil {
		return nil, err
	}
	got, err := gcs.GetGitCredentialByID(ctx, id)
	if err != nil {
		return nil, err
	}
	pub, err := s.toPublic(ctx, got)
	if err != nil {
		return nil, err
	}
	return &pub, nil
}

// Delete removes a credential by id.
func (s *Service) Delete(ctx context.Context, id int) error {
	gcs, err := s.gcs()
	if err != nil {
		return err
	}
	return gcs.DeleteGitCredential(ctx, id)
}

func (s *Service) buildCredential(in CreateInput) (*store.GitCredential, error) {
	hasProject := in.ProjectID != nil
	hasHost := strings.TrimSpace(in.Host) != ""
	if hasProject == hasHost {
		return nil, errors.New("set exactly one of project_id or host")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind != "https" && kind != "ssh" {
		return nil, errors.New("kind must be https or ssh")
	}
	secret := strings.TrimSpace(in.Secret)
	if secret == "" {
		return nil, errors.New("secret is required")
	}
	if err := validateSecret(kind, secret); err != nil {
		return nil, err
	}
	secretEnc, err := s.secrets.Seal([]byte(secret))
	if err != nil {
		return nil, err
	}
	c := &store.GitCredential{
		ProjectID: in.ProjectID, Host: strings.TrimSpace(in.Host),
		Kind: kind, Username: strings.TrimSpace(in.Username),
		SecretEnc: secretEnc, KeyVersion: s.secrets.KeyVersion(),
		SSHKnownHosts: strings.TrimSpace(in.SSHKnownHosts),
		Label:         strings.TrimSpace(in.Label),
	}
	return c, nil
}

func (s *Service) toPublic(ctx context.Context, c *store.GitCredential) (PublicCredential, error) {
	pub := PublicCredential{
		ID: c.ID, Kind: c.Kind, Username: c.Username, Label: c.Label,
		SSHKnownHosts: c.SSHKnownHosts,
		CreatedAt:     c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:     c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if c.ProjectID != nil {
		pub.Scope = "project"
		pub.ProjectID = c.ProjectID
		if p, err := s.store.GetProjectByID(ctx, *c.ProjectID); err == nil && p != nil {
			pub.ProjectName = p.Name
		}
	} else {
		pub.Scope = "host"
		pub.Host = c.Host
	}
	if c.Kind == "ssh" && s.secrets.Enabled() {
		plain, err := s.secrets.Open(c.SecretEnc)
		if err != nil {
			s.log.Error("git credential fingerprint decrypt failed", "id", c.ID, "err", err)
		} else {
			pub.SSHFingerprint = sshFingerprint(plain)
		}
	}
	return pub, nil
}

// validateSecret checks credential plaintext before sealing.
func validateSecret(kind, secret string) error {
	if kind == "ssh" {
		_, err := ssh.ParsePrivateKey([]byte(secret))
		if err == nil {
			return nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "passphrase") {
			return errors.New("passphrase-protected SSH private keys are not supported")
		}
		return fmt.Errorf("invalid SSH private key: %s", sanitizeValidationErr(err))
	}
	if strings.TrimSpace(secret) == "" {
		return errors.New("secret must not be empty")
	}
	return nil
}

func sshFingerprint(pem []byte) string {
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(signer.PublicKey())
}

func sanitizeValidationErr(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ":"); i >= 0 {
		msg = strings.TrimSpace(msg[i+1:])
	}
	return msg
}
