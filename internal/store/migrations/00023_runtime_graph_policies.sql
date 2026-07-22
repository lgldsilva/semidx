-- Runtime communication evidence, per-project privacy policy, and the first
-- tenant quota seam. All rows inherit tenant/workspace scope from the request
-- context; target_project_id is nullable for external services.
-- +goose Up
ALTER TABLE projects ADD COLUMN IF NOT EXISTS privacy_mode TEXT NOT NULL DEFAULT 'hybrid';
UPDATE projects SET privacy_mode = 'hybrid' WHERE privacy_mode IS NULL OR privacy_mode = '';
ALTER TABLE projects ADD CONSTRAINT projects_privacy_mode_check
    CHECK (privacy_mode IN ('cloud', 'hybrid', 'edge'));

CREATE TABLE IF NOT EXISTS runtime_edges (
    id                 BIGSERIAL PRIMARY KEY,
    tenant_id          INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    workspace_id       INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source_project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    target_project_id   INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    target_name         TEXT NOT NULL,
    source_component    TEXT NOT NULL DEFAULT '',
    target_component    TEXT NOT NULL DEFAULT '',
    protocol            TEXT NOT NULL DEFAULT '',
    environment        TEXT NOT NULL DEFAULT '',
    request_count      BIGINT NOT NULL DEFAULT 0,
    error_count        BIGINT NOT NULL DEFAULT 0,
    p95_latency_ms     DOUBLE PRECISION NOT NULL DEFAULT 0,
    first_seen         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_runtime_edges_identity ON runtime_edges
    (tenant_id, workspace_id, source_project_id,
     COALESCE(target_project_id, 0), target_name, source_component,
     target_component, protocol, environment);
CREATE INDEX IF NOT EXISTS idx_runtime_edges_workspace
    ON runtime_edges (tenant_id, workspace_id, source_project_id, last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_runtime_edges_target
    ON runtime_edges (tenant_id, workspace_id, target_project_id);

CREATE TABLE IF NOT EXISTS tenant_quotas (
    tenant_id         INTEGER PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    plan              TEXT NOT NULL DEFAULT 'free',
    max_projects      BIGINT NOT NULL DEFAULT 0,
    max_runtime_edges BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO tenant_quotas (tenant_id)
SELECT id FROM tenants
ON CONFLICT (tenant_id) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS tenant_quotas;
DROP TABLE IF EXISTS runtime_edges;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_privacy_mode_check;
ALTER TABLE projects DROP COLUMN IF EXISTS privacy_mode;
