package localstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/store"
	"github.com/lgldsilva/semidx/internal/tenant"
)

var _ store.RuntimeGraphStore = (*SQLiteStore)(nil)
var _ store.QuotaStore = (*SQLiteStore)(nil)
var _ store.ProjectPolicyStore = (*SQLiteStore)(nil)

func normalizeRuntimeEdge(ctx context.Context, sourceProjectID int, edge store.RuntimeEdge) (store.RuntimeEdge, error) {
	if sourceProjectID <= 0 || strings.TrimSpace(edge.TargetProjectName) == "" {
		return store.RuntimeEdge{}, fmt.Errorf("source project and target are required")
	}
	if edge.RequestCount < 0 || edge.ErrorCount < 0 || edge.P95LatencyMS < 0 {
		return store.RuntimeEdge{}, fmt.Errorf("runtime edge counters must not be negative")
	}
	edge.TenantID = tenant.ID(ctx)
	edge.WorkspaceID = 0
	edge.SourceProjectID = sourceProjectID
	edge.TargetProjectName = strings.TrimSpace(edge.TargetProjectName)
	edge.SourceComponent = strings.TrimSpace(edge.SourceComponent)
	edge.TargetComponent = strings.TrimSpace(edge.TargetComponent)
	edge.Protocol = strings.TrimSpace(edge.Protocol)
	edge.Environment = strings.TrimSpace(edge.Environment)
	if edge.FirstSeen.IsZero() {
		edge.FirstSeen = time.Now().UTC()
	}
	if edge.LastSeen.IsZero() {
		edge.LastSeen = edge.FirstSeen
	}
	return edge, nil
}

func (s *SQLiteStore) UpsertRuntimeEdges(ctx context.Context, sourceProjectID int, edges []store.RuntimeEdge) error {
	if len(edges) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, raw := range edges {
		edge, err := normalizeRuntimeEdge(ctx, sourceProjectID, raw)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO runtime_edges
			(tenant_id, workspace_id, source_project_id, target_project_id, target_name,
			 source_component, target_component, protocol, environment, request_count,
			 error_count, p95_latency_ms, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (tenant_id, workspace_id, source_project_id, target_project_id,
			 target_name, source_component, target_component, protocol, environment)
			DO UPDATE SET request_count = runtime_edges.request_count + excluded.request_count,
			 error_count = runtime_edges.error_count + excluded.error_count,
			 p95_latency_ms = excluded.p95_latency_ms,
			 first_seen = MIN(runtime_edges.first_seen, excluded.first_seen),
			 last_seen = MAX(runtime_edges.last_seen, excluded.last_seen)
		`, edge.TenantID, edge.WorkspaceID, edge.SourceProjectID, edge.TargetProjectID,
			edge.TargetProjectName, edge.SourceComponent, edge.TargetComponent, edge.Protocol,
			edge.Environment, edge.RequestCount, edge.ErrorCount, edge.P95LatencyMS,
			edge.FirstSeen.UTC().Format(time.RFC3339Nano), edge.LastSeen.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

const runtimeSelect = `SELECT e.tenant_id, e.workspace_id, e.source_project_id,
	sp.name, e.target_project_id, COALESCE(tp.name, e.target_name),
	e.source_component, e.target_component, e.protocol, e.environment,
	e.request_count, e.error_count, e.p95_latency_ms, e.first_seen, e.last_seen
	FROM runtime_edges e JOIN projects sp ON sp.id = e.source_project_id
	LEFT JOIN projects tp ON tp.id = e.target_project_id`

func scanRuntimeRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]store.RuntimeEdge, error) {
	var out []store.RuntimeEdge
	for rows.Next() {
		var edge store.RuntimeEdge
		var first, last string
		if err := rows.Scan(&edge.TenantID, &edge.WorkspaceID, &edge.SourceProjectID,
			&edge.SourceProjectName, &edge.TargetProjectID, &edge.TargetProjectName,
			&edge.SourceComponent, &edge.TargetComponent, &edge.Protocol, &edge.Environment,
			&edge.RequestCount, &edge.ErrorCount, &edge.P95LatencyMS, &first, &last); err != nil {
			return nil, err
		}
		edge.FirstSeen, _ = time.Parse(time.RFC3339Nano, first)
		edge.LastSeen, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, edge)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) ListRuntimeEdges(ctx context.Context, projectID int) ([]store.RuntimeEdge, error) {
	rows, err := s.db.QueryContext(ctx, runtimeSelect+` WHERE e.tenant_id = ? AND e.source_project_id = ? ORDER BY e.last_seen DESC, e.target_name`, tenant.ID(ctx), projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRuntimeRows(rows)
}

func (s *SQLiteStore) ListWorkspaceRuntimeEdges(ctx context.Context, limit int) ([]store.RuntimeEdge, error) {
	query := runtimeSelect + ` WHERE e.tenant_id = ? ORDER BY e.last_seen DESC, sp.name, e.target_name`
	args := []any{tenant.ID(ctx)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRuntimeRows(rows)
}

func (s *SQLiteStore) GetTenantQuota(ctx context.Context) (*store.TenantQuota, error) {
	var quota store.TenantQuota
	err := s.db.QueryRowContext(ctx, `SELECT tenant_id, plan, max_projects, max_runtime_edges FROM tenant_quotas WHERE tenant_id = ?`, tenant.ID(ctx)).Scan(&quota.TenantID, &quota.Plan, &quota.MaxProjects, &quota.MaxRuntimeEdges)
	return &quota, err
}

func (s *SQLiteStore) SetTenantQuota(ctx context.Context, quota store.TenantQuota) error {
	if quota.Plan == "" {
		quota.Plan = "custom"
	}
	if quota.MaxProjects < 0 || quota.MaxRuntimeEdges < 0 {
		return fmt.Errorf("quota limits must be zero or positive")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO tenant_quotas (tenant_id, plan, max_projects, max_runtime_edges)
		VALUES (?, ?, ?, ?) ON CONFLICT(tenant_id) DO UPDATE SET plan=excluded.plan,
		max_projects=excluded.max_projects, max_runtime_edges=excluded.max_runtime_edges,
		updated_at=datetime('now')`, tenant.ID(ctx), quota.Plan, quota.MaxProjects, quota.MaxRuntimeEdges)
	return err
}

func (s *SQLiteStore) GetTenantUsage(ctx context.Context) (*store.TenantUsage, error) {
	var usage store.TenantUsage
	err := s.db.QueryRowContext(ctx, `SELECT ?,
		(SELECT COUNT(*) FROM projects),
		(SELECT COUNT(*) FROM runtime_edges WHERE tenant_id = ?)`, tenant.ID(ctx), tenant.ID(ctx)).Scan(&usage.TenantID, &usage.Projects, &usage.RuntimeEdges)
	return &usage, err
}
