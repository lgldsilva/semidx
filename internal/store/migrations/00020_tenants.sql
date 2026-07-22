-- Multi-tenant foundation. Existing installations are migrated into the
-- compatibility tenant so old CLI/server flows keep working while every new
-- project and token has an explicit tenant boundary.
-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
    id SERIAL PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO tenants (id, slug, name)
VALUES (1, 'default', 'Default organization')
ON CONFLICT (id) DO NOTHING;

SELECT setval(
    pg_get_serial_sequence('tenants', 'id'),
    GREATEST((SELECT COALESCE(MAX(id), 1) FROM tenants), 1),
    true
);

CREATE TABLE IF NOT EXISTS tenant_memberships (
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_tenant_memberships_user ON tenant_memberships(user_id, tenant_id);

ALTER TABLE projects ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE projects SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE projects ALTER COLUMN tenant_id SET DEFAULT 1;
ALTER TABLE projects ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_tenant_fk;
ALTER TABLE projects ADD CONSTRAINT projects_tenant_fk FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
DROP INDEX IF EXISTS projects_identity_idx;
CREATE UNIQUE INDEX IF NOT EXISTS projects_tenant_identity_idx ON projects (tenant_id, identity);
CREATE INDEX IF NOT EXISTS projects_tenant_idx ON projects (tenant_id, name);

ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE api_tokens SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE api_tokens ALTER COLUMN tenant_id SET DEFAULT 1;
ALTER TABLE api_tokens ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE api_tokens DROP CONSTRAINT IF EXISTS api_tokens_tenant_fk;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_tenant_fk FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS api_tokens_tenant_idx ON api_tokens (tenant_id, revoked_at);

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE conversations SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE conversations ALTER COLUMN tenant_id SET DEFAULT 1;
ALTER TABLE conversations ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_tenant_fk;
ALTER TABLE conversations ADD CONSTRAINT conversations_tenant_fk FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_conversations_tenant_user ON conversations (tenant_id, user_id, updated_at DESC);

ALTER TABLE git_credentials ADD COLUMN IF NOT EXISTS tenant_id INTEGER;
UPDATE git_credentials SET tenant_id = 1 WHERE tenant_id IS NULL;
ALTER TABLE git_credentials ALTER COLUMN tenant_id SET DEFAULT 1;
ALTER TABLE git_credentials ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE git_credentials DROP CONSTRAINT IF EXISTS git_credentials_tenant_fk;
ALTER TABLE git_credentials ADD CONSTRAINT git_credentials_tenant_fk FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
DROP INDEX IF EXISTS idx_git_credentials_project;
DROP INDEX IF EXISTS idx_git_credentials_host;
CREATE UNIQUE INDEX IF NOT EXISTS idx_git_credentials_tenant_project
    ON git_credentials (tenant_id, project_id) WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_git_credentials_tenant_host
    ON git_credentials (tenant_id, lower(host)) WHERE project_id IS NULL;

INSERT INTO tenant_memberships (tenant_id, user_id, role)
SELECT 1, id, CASE WHEN role = 'admin' THEN 'owner' ELSE 'member' END
FROM users
ON CONFLICT (tenant_id, user_id) DO NOTHING;

-- +goose Down
DROP INDEX IF EXISTS idx_git_credentials_tenant_host;
DROP INDEX IF EXISTS idx_git_credentials_tenant_project;
ALTER TABLE git_credentials DROP CONSTRAINT IF EXISTS git_credentials_tenant_fk;
ALTER TABLE git_credentials DROP COLUMN IF EXISTS tenant_id;
DROP INDEX IF EXISTS idx_conversations_tenant_user;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_tenant_fk;
ALTER TABLE conversations DROP COLUMN IF EXISTS tenant_id;
DROP INDEX IF EXISTS api_tokens_tenant_idx;
ALTER TABLE api_tokens DROP CONSTRAINT IF EXISTS api_tokens_tenant_fk;
ALTER TABLE api_tokens DROP COLUMN IF EXISTS tenant_id;
DROP INDEX IF EXISTS projects_tenant_idx;
DROP INDEX IF EXISTS projects_tenant_identity_idx;
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_tenant_fk;
ALTER TABLE projects DROP COLUMN IF EXISTS tenant_id;
DROP TABLE IF EXISTS tenant_memberships;
DROP TABLE IF EXISTS tenants;
