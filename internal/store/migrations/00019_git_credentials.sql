-- +goose Up
-- Git credentials for private-repo cloning: a credential is scoped to exactly
-- one project (overrides) or one host (fallback). secret_enc is opaque
-- ciphertext produced by the caller (internal/secretbox); the store never sees
-- plaintext. key_version tracks which encryption key sealed the secret.
CREATE TABLE IF NOT EXISTS git_credentials (
    id SERIAL PRIMARY KEY,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE, -- NULL = host-scoped credential
    host TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL CHECK (kind IN ('https','ssh')),
    username TEXT NOT NULL DEFAULT '',
    secret_enc BYTEA NOT NULL,
    key_version INTEGER NOT NULL DEFAULT 1,
    ssh_known_hosts TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT git_credentials_scope CHECK (
        (project_id IS NOT NULL AND host = '') OR (project_id IS NULL AND host <> '')
    )
);
-- At most one credential per project and one per host (case-insensitive).
CREATE UNIQUE INDEX IF NOT EXISTS idx_git_credentials_project
    ON git_credentials (project_id) WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_git_credentials_host
    ON git_credentials (lower(host)) WHERE project_id IS NULL;

-- +goose Down
DROP TABLE IF EXISTS git_credentials;
