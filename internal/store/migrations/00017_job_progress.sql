-- +goose Up
-- Live job progress for admin polling (files scanned vs total).
ALTER TABLE index_jobs ADD COLUMN IF NOT EXISTS progress_total INTEGER NOT NULL DEFAULT 0;
ALTER TABLE index_jobs ADD COLUMN IF NOT EXISTS progress_done INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE index_jobs DROP COLUMN IF EXISTS progress_done;
ALTER TABLE index_jobs DROP COLUMN IF EXISTS progress_total;
