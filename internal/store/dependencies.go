package store

import (
	"context"

	"github.com/lgldsilva/semidx/internal/tenant"
)

// Dependency is a normalized manifest declaration. A blank ResolvedVersion
// means that only the declared constraint is known.
type Dependency struct {
	Ecosystem       string
	Name            string
	NormalizedName  string
	Constraint      string
	ResolvedVersion string
	Scope           string
	Source          string
	Manifest        string
	Direct          bool
}

// DependencyUsage identifies a dependency occurrence in another project.
type DependencyUsage struct {
	ProjectID       int
	TenantID        int
	ProjectName     string
	Ecosystem       string
	Name            string
	NormalizedName  string
	Constraint      string
	ResolvedVersion string
	Scope           string
	Direct          bool
}

// DependencyStore is optional so existing IndexStore fakes and third-party
// implementations remain source-compatible. PgStore and SQLiteStore implement
// it, which keeps local and SaaS indexing on the same dependency contract.
type DependencyStore interface {
	ReplaceProjectDependencies(context.Context, int, []Dependency) error
	ListProjectDependencies(context.Context, int) ([]Dependency, error)
	FindProjectsSharingDependency(context.Context, int) ([]DependencyUsage, error)
}

var _ DependencyStore = (*PgStore)(nil)

func (s *PgStore) ReplaceProjectDependencies(ctx context.Context, projectID int, deps []Dependency) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM project_dependencies WHERE tenant_id = $1 AND project_id = $2`, tenant.ID(ctx), projectID); err != nil {
		return err
	}
	for _, dep := range deps {
		_, err := tx.Exec(ctx, `
			INSERT INTO project_dependencies
			(tenant_id, project_id, ecosystem, name, normalized_name, constraint_text,
			 resolved_version, scope, source, manifest, direct)
			SELECT $1, p.id, $3, $4, $5, $6, $7, $8, $9, $10, $11
			FROM projects p WHERE p.id = $2 AND p.tenant_id = $1
			ON CONFLICT (tenant_id, project_id, ecosystem, normalized_name, scope)
			DO UPDATE SET name = EXCLUDED.name, constraint_text = EXCLUDED.constraint_text,
			 resolved_version = EXCLUDED.resolved_version, source = EXCLUDED.source,
			 manifest = EXCLUDED.manifest, direct = EXCLUDED.direct, observed_at = NOW()
		`, tenant.ID(ctx), projectID, dep.Ecosystem, dep.Name, dep.NormalizedName,
			dep.Constraint, dep.ResolvedVersion, dep.Scope, dep.Source, dep.Manifest, dep.Direct)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ListProjectDependencies(ctx context.Context, projectID int) ([]Dependency, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ecosystem, name, normalized_name, constraint_text, resolved_version,
			scope, source, manifest, direct
		FROM project_dependencies
		WHERE tenant_id = $1 AND project_id = $2
		ORDER BY ecosystem, normalized_name, scope`, tenant.ID(ctx), projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dependency
	for rows.Next() {
		var dep Dependency
		if err := rows.Scan(&dep.Ecosystem, &dep.Name, &dep.NormalizedName, &dep.Constraint,
			&dep.ResolvedVersion, &dep.Scope, &dep.Source, &dep.Manifest, &dep.Direct); err != nil {
			return nil, err
		}
		out = append(out, dep)
	}
	return out, rows.Err()
}

func (s *PgStore) FindProjectsSharingDependency(ctx context.Context, projectID int) ([]DependencyUsage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT p2.id, p2.tenant_id, p2.name, d2.ecosystem, d2.name,
			d2.normalized_name, d2.constraint_text, d2.resolved_version, d2.scope, d2.direct
		FROM project_dependencies d1
		JOIN project_dependencies d2
		  ON d2.tenant_id = d1.tenant_id
		 AND d2.ecosystem = d1.ecosystem
		 AND d2.normalized_name = d1.normalized_name
		JOIN projects p2 ON p2.id = d2.project_id AND p2.tenant_id = d2.tenant_id
		WHERE d1.tenant_id = $1 AND d1.project_id = $2 AND d2.project_id <> $2
		  AND ($3 = 0 OR p2.workspace_id = $3)
		ORDER BY p2.name, d2.ecosystem, d2.normalized_name`, tenant.ID(ctx), projectID, activeWorkspaceID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DependencyUsage
	for rows.Next() {
		var usage DependencyUsage
		if err := rows.Scan(&usage.ProjectID, &usage.TenantID, &usage.ProjectName, &usage.Ecosystem,
			&usage.Name, &usage.NormalizedName, &usage.Constraint, &usage.ResolvedVersion,
			&usage.Scope, &usage.Direct); err != nil {
			return nil, err
		}
		out = append(out, usage)
	}
	return out, rows.Err()
}
