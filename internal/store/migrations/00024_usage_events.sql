-- +goose Up
-- Product usage analytics: one row per search attempt (no query text by default).
CREATE TABLE IF NOT EXISTS usage_events (
    id          BIGSERIAL PRIMARY KEY,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    project     TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'unknown',
    outcome     TEXT NOT NULL DEFAULT 'ok',
    hit_count   INTEGER NOT NULL DEFAULT 0,
    latency_ms  BIGINT NOT NULL DEFAULT 0,
    keyword     BOOLEAN NOT NULL DEFAULT false,
    graph       BOOLEAN NOT NULL DEFAULT false,
    query_hash  TEXT NOT NULL DEFAULT '',
    query_text  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_usage_events_ts ON usage_events (ts DESC);
CREATE INDEX IF NOT EXISTS idx_usage_events_project_ts ON usage_events (project, ts DESC);

-- +goose Down
DROP TABLE IF EXISTS usage_events;
