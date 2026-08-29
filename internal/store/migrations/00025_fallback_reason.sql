-- +goose Up
-- Why a search fell back to keyword: a low-cardinality "provider: class"
-- string (e.g. "ollama: timeout"), '' for non-fallback events.
ALTER TABLE usage_events ADD COLUMN IF NOT EXISTS fallback_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE usage_events DROP COLUMN IF EXISTS fallback_reason;
