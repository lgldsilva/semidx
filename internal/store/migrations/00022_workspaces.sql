-- Workspace is the project portfolio boundary inside a tenant. Existing
-- installations receive one compatibility workspace per tenant.
-- +goose Up
CREATE TABLE IF NOT EXISTS workspaces (
    id SERIAL PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, slug)
);

INSERT INTO workspaces (tenant_id, slug, name)
SELECT id, 'default', 'Default workspace' FROM tenants
ON CONFLICT (tenant_id, slug) DO NOTHING;

ALTER TABLE projects ADD COLUMN IF NOT EXISTS workspace_id INTEGER;
UPDATE projects p
SET workspace_id = w.id
FROM workspaces w
WHERE w.tenant_id = p.tenant_id AND w.slug = 'default' AND p.workspace_id IS NULL;
ALTER TABLE projects ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_workspace_fk;
ALTER TABLE projects ADD CONSTRAINT projects_workspace_fk
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE;

DROP INDEX IF EXISTS projects_tenant_identity_idx;
CREATE UNIQUE INDEX IF NOT EXISTS projects_workspace_identity_idx
    ON projects (workspace_id, identity);
CREATE INDEX IF NOT EXISTS projects_workspace_name_idx
    ON projects (workspace_id, name);

-- +goose Down
DROP INDEX IF EXISTS projects_workspace_name_idx;
DROP INDEX IF EXISTS projects_workspace_identity_idx;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_workspace_fk;
ALTER TABLE projects DROP COLUMN IF EXISTS workspace_id;
DROP TABLE IF EXISTS workspaces;
