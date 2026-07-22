package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lgldsilva/semidx/internal/tenant"
)

var _ RuntimeGraphStore = (*PgStore)(nil)
var _ QuotaStore = (*PgStore)(nil)
var _ ProjectPolicyStore = (*PgStore)(nil)

func runtimeEdgeValues(ctx context.Context, sourceProjectID int, edge RuntimeEdge) (any, error) {
	if sourceProjectID <= 0 {
		return nil, fmt.Errorf("source project id must be positive")
	}
	if strings.TrimSpace(edge.TargetProjectName) == "" {
		return nil, fmt.Errorf("runtime edge target is required")
	}
	if edge.RequestCount < 0 || edge.ErrorCount < 0 || edge.P95LatencyMS < 0 {
		return nil, fmt.Errorf("runtime edge counters must not be negative")
	}
	first, last := edge.FirstSeen, edge.LastSeen
	if first.IsZero() {
		first = time.Now().UTC()
	}
	if last.IsZero() {
		last = first
	}
	if last.Before(first) {
		first, last = last, first
	}
	return []any{tenant.ID(ctx), activeWorkspaceID(ctx), sourceProjectID,
		nullInt(edge.TargetProjectID), strings.TrimSpace(edge.TargetProjectName),
		strings.TrimSpace(edge.SourceComponent), strings.TrimSpace(edge.TargetComponent),
		strings.TrimSpace(edge.Protocol), strings.TrimSpace(edge.Environment),
		edge.RequestCount, edge.ErrorCount, edge.P95LatencyMS, first, last}, nil
}

func nullInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func (s *PgStore) UpsertRuntimeEdges(ctx context.Context, sourceProjectID int, edges []RuntimeEdge) error {
	if len(edges) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, edge := range edges {
		values, err := runtimeEdgeValues(ctx, sourceProjectID, edge)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO runtime_edges
			(tenant_id, workspace_id, source_project_id, target_project_id, target_name,
			 source_component, target_component, protocol, environment, request_count,
			 error_count, p95_latency_ms, first_seen, last_seen)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			ON CONFLICT DO UPDATE SET
			 request_count = runtime_edges.request_count + EXCLUDED.request_count,
			 error_count = runtime_edges.error_count + EXCLUDED.error_count,
			 p95_latency_ms = EXCLUDED.p95_latency_ms,
			 last_seen = GREATEST(runtime_edges.last_seen, EXCLUDED.last_seen),
			 first_seen = LEAST(runtime_edges.first_seen, EXCLUDED.first_seen)
		`, values.([]any)...)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const runtimeEdgeColumns = `e.tenant_id, e.workspace_id, e.source_project_id,
	sp.name, COALESCE(e.target_project_id, 0), COALESCE(tp.name, e.target_name),
	e.source_component, e.target_component, e.protocol, e.environment,
	e.request_count, e.error_count, e.p95_latency_ms, e.first_seen, e.last_seen`

func scanRuntimeEdges(rows pgx.Rows) ([]RuntimeEdge, error) {
	var out []RuntimeEdge
	for rows.Next() {
		var edge RuntimeEdge
		if err := rows.Scan(&edge.TenantID, &edge.WorkspaceID, &edge.SourceProjectID,
			&edge.SourceProjectName, &edge.TargetProjectID, &edge.TargetProjectName,
			&edge.SourceComponent, &edge.TargetComponent, &edge.Protocol, &edge.Environment,
			&edge.RequestCount, &edge.ErrorCount, &edge.P95LatencyMS, &edge.FirstSeen, &edge.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, edge)
	}
	return out, rows.Err()
}

func (s *PgStore) ListRuntimeEdges(ctx context.Context, projectID int) ([]RuntimeEdge, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+runtimeEdgeColumns+` FROM runtime_edges e
		JOIN projects sp ON sp.id = e.source_project_id
		LEFT JOIN projects tp ON tp.id = e.target_project_id
		WHERE e.tenant_id = $1 AND e.workspace_id = $2 AND e.source_project_id = $3
		ORDER BY e.last_seen DESC, target_name`, tenant.ID(ctx), activeWorkspaceID(ctx), projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuntimeEdges(rows)
}

func (s *PgStore) ListWorkspaceRuntimeEdges(ctx context.Context, limit int) ([]RuntimeEdge, error) {
	args := []any{tenant.ID(ctx), activeWorkspaceID(ctx)}
	limitSQL := ""
	if limit > 0 {
		args = append(args, limit)
		limitSQL = " LIMIT $3"
	}
	rows, err := s.pool.Query(ctx, `SELECT `+runtimeEdgeColumns+` FROM runtime_edges e
		JOIN projects sp ON sp.id = e.source_project_id
		LEFT JOIN projects tp ON tp.id = e.target_project_id
		WHERE e.tenant_id = $1 AND ($2 = 0 OR e.workspace_id = $2)
		ORDER BY e.last_seen DESC, sp.name, target_name`+limitSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuntimeEdges(rows)
}

func (s *PgStore) GetTenantQuota(ctx context.Context) (*TenantQuota, error) {
	var quota TenantQuota
	err := s.pool.QueryRow(ctx, `SELECT tenant_id, plan, max_projects, max_runtime_edges
		FROM tenant_quotas WHERE tenant_id = $1`, tenant.ID(ctx)).Scan(&quota.TenantID, &quota.Plan, &quota.MaxProjects, &quota.MaxRuntimeEdges)
	if err != nil {
		return nil, err
	}
	return &quota, nil
}

func (s *PgStore) SetTenantQuota(ctx context.Context, quota TenantQuota) error {
	if quota.Plan == "" {
		quota.Plan = "custom"
	}
	if quota.MaxProjects < 0 || quota.MaxRuntimeEdges < 0 {
		return fmt.Errorf("quota limits must be zero or positive")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO tenant_quotas (tenant_id, plan, max_projects, max_runtime_edges)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (tenant_id) DO UPDATE SET plan=EXCLUDED.plan,
		max_projects=EXCLUDED.max_projects, max_runtime_edges=EXCLUDED.max_runtime_edges,
		updated_at=NOW()`, tenant.ID(ctx), quota.Plan, quota.MaxProjects, quota.MaxRuntimeEdges)
	return err
}

func (s *PgStore) GetTenantUsage(ctx context.Context) (*TenantUsage, error) {
	var usage TenantUsage
	err := s.pool.QueryRow(ctx, `
		SELECT $1,
		 (SELECT COUNT(*) FROM projects WHERE tenant_id = $1),
		 (SELECT COUNT(*) FROM runtime_edges WHERE tenant_id = $1)`, tenant.ID(ctx)).Scan(&usage.TenantID, &usage.Projects, &usage.RuntimeEdges)
	if err != nil {
		return nil, err
	}
	return &usage, nil
}
