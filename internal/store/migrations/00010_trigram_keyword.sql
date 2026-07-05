-- +goose Up
-- Enable pg_trgm extension for GIN-indexed ILIKE keyword search
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
-- pg_trgm can't be dropped if anything depends on it, so this is a no-op.
