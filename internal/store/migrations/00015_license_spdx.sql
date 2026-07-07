-- +goose Up
ALTER TABLE projects ADD COLUMN IF NOT EXISTS license_spdx_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE projects DROP COLUMN IF EXISTS license_spdx_id;
