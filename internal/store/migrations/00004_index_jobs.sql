-- +goose Up
-- Durable indexing jobs: enqueued via the API, claimed by server workers with
-- FOR UPDATE SKIP LOCKED so a job survives a worker/container restart and is
-- never run by two workers at once.
CREATE TABLE IF NOT EXISTS index_jobs (
    id SERIAL PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    type TEXT NOT NULL DEFAULT 'full',
    status TEXT NOT NULL DEFAULT 'queued', -- queued | running | succeeded | failed
    error TEXT,
    files_indexed INTEGER NOT NULL DEFAULT 0,
    chunks_created INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    started_at TIMESTAMP,
    finished_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_index_jobs_queued ON index_jobs (id) WHERE status = 'queued';

-- +goose Down
DROP TABLE IF EXISTS index_jobs;
