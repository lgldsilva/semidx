-- +goose Up
-- Switch web_sessions timestamp columns to TIMESTAMPTZ so comparisons against
-- NOW() and Go-supplied time.Time parameters are timezone-safe. The host and
-- the PostgreSQL container may run in different timezones; TIMESTAMPTZ
-- normalises everything to UTC internally and guarantees correct "is this
-- session expired?" checks regardless of session timezone.
ALTER TABLE web_sessions ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC';
ALTER TABLE web_sessions ALTER COLUMN expires_at TYPE TIMESTAMPTZ USING expires_at AT TIME ZONE 'UTC';

-- +goose Down
ALTER TABLE web_sessions ALTER COLUMN created_at TYPE TIMESTAMP;
ALTER TABLE web_sessions ALTER COLUMN expires_at TYPE TIMESTAMP;
