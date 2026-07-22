-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_dependencies (
    tenant_id        INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    project_id       INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    ecosystem        TEXT NOT NULL,
    name             TEXT NOT NULL,
    normalized_name  TEXT NOT NULL,
    constraint_text  TEXT NOT NULL DEFAULT '',
    resolved_version TEXT NOT NULL DEFAULT '',
    scope            TEXT NOT NULL DEFAULT 'runtime',
    source           TEXT NOT NULL DEFAULT '',
    manifest        TEXT NOT NULL DEFAULT '',
    direct          BOOLEAN NOT NULL DEFAULT TRUE,
    observed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, project_id, ecosystem, normalized_name, scope)
);
CREATE INDEX IF NOT EXISTS idx_project_dependencies_lookup
    ON project_dependencies (tenant_id, ecosystem, normalized_name);
CREATE INDEX IF NOT EXISTS idx_project_dependencies_project
    ON project_dependencies (tenant_id, project_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS project_dependencies;
-- +goose StatementEnd
