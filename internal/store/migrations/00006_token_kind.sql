-- +goose Up
-- Distinguish opaque API keys from JWT control tokens sharing this table, and
-- record a display expiry (NULL = never). For JWTs the token_hash column holds
-- the jti, so the existing revocation/lookup path works unchanged.
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'opaque';
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS expires_at TIMESTAMP;

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN IF EXISTS expires_at;
ALTER TABLE api_tokens DROP COLUMN IF EXISTS kind;
