package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lgldsilva/semidx/internal/tenant"
)

// Tenant is an organization boundary in the hosted and self-hosted control
// plane.
type Tenant struct {
	ID        int
	Slug      string
	Name      string
	CreatedAt time.Time
}

// Membership grants a user access to one tenant.
type Membership struct {
	TenantID int
	UserID   int
	Role     string // owner | admin | member | viewer
}

// Workspace is the project portfolio boundary inside a tenant.
type Workspace struct {
	ID        int
	TenantID  int
	Slug      string
	Name      string
	CreatedAt time.Time
}

// TenantStore is optional so SQLite and existing test doubles remain valid.
// PostgreSQL implements the complete server-side tenant control plane.
type TenantStore interface {
	ListTenants(ctx context.Context) ([]Tenant, error)
	GetTenantByID(ctx context.Context, id int) (*Tenant, error)
	GetTenantBySlug(ctx context.Context, slug string) (*Tenant, error)
	CreateTenant(ctx context.Context, slug, name string) (*Tenant, error)
	ListMemberships(ctx context.Context, userID int) ([]Membership, error)
	UpsertMembership(ctx context.Context, membership Membership) error
	CanAccessTenant(ctx context.Context, userID, tenantID int) (bool, error)
}

// WorkspaceStore is optional for backwards-compatible stores. PostgreSQL
// implements it; standalone SQLite remains a single implicit workspace.
type WorkspaceStore interface {
	ListWorkspaces(ctx context.Context) ([]Workspace, error)
	GetWorkspaceBySlug(ctx context.Context, slug string) (*Workspace, error)
	CreateWorkspace(ctx context.Context, slug, name string) (*Workspace, error)
}

var (
	ErrTenantExists    = errors.New("tenant already exists")
	ErrWorkspaceExists = errors.New("workspace already exists")
	ErrInvalidRole     = errors.New("invalid tenant role")
)

const tenantColumns = `id, slug, name, created_at`

func scanTenant(row pgx.Row) (*Tenant, error) {
	var t Tenant
	if err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *PgStore) ListTenants(ctx context.Context) ([]Tenant, error) {
	tc, ok := tenant.From(ctx)
	if tc.GlobalAdmin && ok {
		return s.listAllTenants(ctx)
	}
	if !ok || tc.UserID <= 0 {
		one, err := s.GetTenantByID(ctx, tenant.ID(ctx))
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []Tenant{*one}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.slug, t.name, t.created_at
		FROM tenants t JOIN tenant_memberships m ON m.tenant_id = t.id
		WHERE m.user_id = $1 ORDER BY t.slug`, tc.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PgStore) listAllTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+tenantColumns+` FROM tenants ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *PgStore) GetTenantByID(ctx context.Context, id int) (*Tenant, error) {
	t, err := scanTenant(s.pool.QueryRow(ctx, `SELECT `+tenantColumns+` FROM tenants WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *PgStore) GetTenantBySlug(ctx context.Context, slug string) (*Tenant, error) {
	t, err := scanTenant(s.pool.QueryRow(ctx, `SELECT `+tenantColumns+` FROM tenants WHERE slug = $1`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *PgStore) CreateTenant(ctx context.Context, slug, name string) (*Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tenants (slug, name) VALUES ($1, $2)
		RETURNING `+tenantColumns, slug, name).Scan(&t.ID, &t.Slug, &t.Name, &t.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrTenantExists
		}
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO workspaces (tenant_id, slug, name)
		VALUES ($1, 'default', 'Default workspace') ON CONFLICT (tenant_id, slug) DO NOTHING`, t.ID); err != nil {
		return nil, err
	}
	if tc, ok := tenant.From(ctx); ok && tc.UserID > 0 {
		if err := s.UpsertMembership(ctx, Membership{TenantID: t.ID, UserID: tc.UserID, Role: "owner"}); err != nil {
			return nil, err
		}
	}
	return &t, nil
}

func validateMembershipRole(role string) error {
	switch role {
	case "owner", "admin", "member", "viewer":
		return nil
	default:
		return ErrInvalidRole
	}
}

func (s *PgStore) ListMemberships(ctx context.Context, userID int) ([]Membership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tenant_id, user_id, role FROM tenant_memberships
		WHERE user_id = $1 ORDER BY tenant_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.TenantID, &m.UserID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PgStore) UpsertMembership(ctx context.Context, m Membership) error {
	if m.TenantID <= 0 || m.UserID <= 0 {
		return errors.New("tenant membership requires tenant and user")
	}
	if err := validateMembershipRole(m.Role); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		m.TenantID, m.UserID, m.Role)
	return err
}

func (s *PgStore) CanAccessTenant(ctx context.Context, userID, tenantID int) (bool, error) {
	if userID <= 0 || tenantID <= 0 {
		return false, nil
	}
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM tenant_memberships WHERE user_id = $1 AND tenant_id = $2)`,
		userID, tenantID).Scan(&ok)
	return ok, err
}

const workspaceColumns = `id, tenant_id, slug, name, created_at`

func scanWorkspace(row pgx.Row) (*Workspace, error) {
	var w Workspace
	if err := row.Scan(&w.ID, &w.TenantID, &w.Slug, &w.Name, &w.CreatedAt); err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *PgStore) ListWorkspaces(ctx context.Context) ([]Workspace, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+workspaceColumns+`
		FROM workspaces WHERE tenant_id = $1 ORDER BY slug`, tenant.ID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.TenantID, &w.Slug, &w.Name, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *PgStore) GetWorkspaceBySlug(ctx context.Context, slug string) (*Workspace, error) {
	w, err := scanWorkspace(s.pool.QueryRow(ctx, `SELECT `+workspaceColumns+`
		FROM workspaces WHERE tenant_id = $1 AND slug = $2`, tenant.ID(ctx), slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return w, err
}

func (s *PgStore) CreateWorkspace(ctx context.Context, slug, name string) (*Workspace, error) {
	var w Workspace
	err := s.pool.QueryRow(ctx, `INSERT INTO workspaces (tenant_id, slug, name)
		VALUES ($1, $2, $3) RETURNING `+workspaceColumns, tenant.ID(ctx), slug, name).
		Scan(&w.ID, &w.TenantID, &w.Slug, &w.Name, &w.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrWorkspaceExists
		}
		return nil, err
	}
	return &w, nil
}
