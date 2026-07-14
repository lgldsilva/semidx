package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrCredentialExists is returned when a git credential already exists for the
// same scope (the project, or the host case-insensitively).
var ErrCredentialExists = errors.New("git credential already exists for this scope")

// GitCredential is a stored credential for cloning/pulling private git repos.
// Exactly one of ProjectID and Host is set: a project-scoped credential takes
// precedence, a host-scoped one is the fallback for every repo on that host.
// SecretEnc is opaque ciphertext (sealed by the caller before it reaches the
// store); KeyVersion records which encryption key sealed it so keys can rotate.
type GitCredential struct {
	ID            int
	ProjectID     *int // nil = host-scoped credential
	Host          string
	Kind          string // "https" (token/password) | "ssh" (private key)
	Username      string
	SecretEnc     []byte
	KeyVersion    int
	SSHKnownHosts string
	Label         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// GitCredentialStore persists git credentials. It is an optional extension
// implemented by PgStore; callers type-assert it, so stores that don't support
// it (SQLite, remote client) simply disable the feature.
type GitCredentialStore interface {
	CreateGitCredential(ctx context.Context, c *GitCredential) (*GitCredential, error)
	// GetGitCredentialForProject returns the project-scoped credential, or
	// ErrNotFound (it does NOT fall back to the host credential — resolution
	// order is the caller's concern).
	GetGitCredentialForProject(ctx context.Context, projectID int) (*GitCredential, error)
	// GetGitCredentialForHost matches the host case-insensitively.
	GetGitCredentialForHost(ctx context.Context, host string) (*GitCredential, error)
	ListGitCredentials(ctx context.Context) ([]GitCredential, error)
	// UpdateGitCredential replaces the mutable fields (kind, username, secret,
	// key version, known hosts, label) of the credential identified by c.ID and
	// bumps updated_at. The scope (project/host) is immutable.
	UpdateGitCredential(ctx context.Context, c *GitCredential) error
	DeleteGitCredential(ctx context.Context, id int) error
}

var _ GitCredentialStore = (*PgStore)(nil)

// #nosec G101 -- SQL column list, not a hardcoded credential.
const gitCredentialColumns = `id, project_id, host, kind, username, secret_enc,
	key_version, ssh_known_hosts, label, created_at, updated_at`

// scanFields returns scan destinations matching gitCredentialColumns order.
func (c *GitCredential) scanFields() []any {
	return []any{&c.ID, &c.ProjectID, &c.Host, &c.Kind, &c.Username, &c.SecretEnc,
		&c.KeyVersion, &c.SSHKnownHosts, &c.Label, &c.CreatedAt, &c.UpdatedAt}
}

// validateGitCredentialFields checks the scope-independent invariants shared
// by Create and Update.
func validateGitCredentialFields(c *GitCredential) error {
	if c.Kind != "https" && c.Kind != "ssh" {
		return fmt.Errorf("invalid git credential kind %q (want https or ssh)", c.Kind)
	}
	if len(c.SecretEnc) == 0 {
		return errors.New("git credential secret must not be empty")
	}
	return nil
}

// CreateGitCredential inserts a credential scoped to exactly one of project or
// host. Returns ErrCredentialExists when that scope already has a credential.
func (s *PgStore) CreateGitCredential(ctx context.Context, c *GitCredential) (*GitCredential, error) {
	if (c.ProjectID != nil) == (c.Host != "") {
		return nil, errors.New("git credential must set exactly one of project or host")
	}
	if err := validateGitCredentialFields(c); err != nil {
		return nil, err
	}
	keyVersion := c.KeyVersion
	if keyVersion <= 0 {
		keyVersion = 1
	}
	var out GitCredential
	err := s.pool.QueryRow(ctx, `
		INSERT INTO git_credentials
			(project_id, host, kind, username, secret_enc, key_version, ssh_known_hosts, label)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+gitCredentialColumns,
		c.ProjectID, c.Host, c.Kind, c.Username, c.SecretEnc, keyVersion,
		c.SSHKnownHosts, c.Label).Scan(out.scanFields()...)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation (scope)
			return nil, ErrCredentialExists
		}
		return nil, err
	}
	return &out, nil
}

// GetGitCredentialForProject returns the credential bound to projectID, or
// ErrNotFound.
func (s *PgStore) GetGitCredentialForProject(ctx context.Context, projectID int) (*GitCredential, error) {
	var c GitCredential
	err := s.pool.QueryRow(ctx,
		`SELECT `+gitCredentialColumns+` FROM git_credentials WHERE project_id = $1`,
		projectID).Scan(c.scanFields()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetGitCredentialForHost returns the host-scoped credential for host
// (case-insensitive), or ErrNotFound.
func (s *PgStore) GetGitCredentialForHost(ctx context.Context, host string) (*GitCredential, error) {
	var c GitCredential
	err := s.pool.QueryRow(ctx, `
		SELECT `+gitCredentialColumns+` FROM git_credentials
		WHERE project_id IS NULL AND lower(host) = lower($1)`,
		host).Scan(c.scanFields()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListGitCredentials returns all credentials, host-scoped first then by id.
func (s *PgStore) ListGitCredentials(ctx context.Context) ([]GitCredential, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+gitCredentialColumns+` FROM git_credentials
		ORDER BY project_id NULLS FIRST, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GitCredential
	for rows.Next() {
		var c GitCredential
		if err := rows.Scan(c.scanFields()...); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpdateGitCredential replaces the mutable fields of the credential c.ID and
// bumps updated_at. Returns ErrNotFound when no such credential exists.
func (s *PgStore) UpdateGitCredential(ctx context.Context, c *GitCredential) error {
	if err := validateGitCredentialFields(c); err != nil {
		return err
	}
	keyVersion := c.KeyVersion
	if keyVersion <= 0 {
		keyVersion = 1
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE git_credentials
		SET kind = $1, username = $2, secret_enc = $3, key_version = $4,
			ssh_known_hosts = $5, label = $6, updated_at = NOW()
		WHERE id = $7`,
		c.Kind, c.Username, c.SecretEnc, keyVersion, c.SSHKnownHosts, c.Label, c.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteGitCredential removes a credential by id, or returns ErrNotFound.
func (s *PgStore) DeleteGitCredential(ctx context.Context, id int) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM git_credentials WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
