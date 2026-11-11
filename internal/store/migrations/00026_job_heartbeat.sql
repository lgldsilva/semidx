-- +goose Up
-- Liveness for running jobs: workers refresh heartbeat_at while a job runs, so
-- the periodic stale sweep requeues only jobs whose worker actually died, not
-- long-running ones. Same type as started_at (TIMESTAMP) for direct comparison.
ALTER TABLE index_jobs ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMP;

-- +goose Down
ALTER TABLE index_jobs DROP COLUMN IF EXISTS heartbeat_at;
