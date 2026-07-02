-- +goose Up
-- API tokens for authenticating clients to the server. Only the SHA-256 hash of
-- each token is stored; the plaintext is shown once at creation time.
CREATE TABLE IF NOT EXISTS api_tokens (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    scopes TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMP,
    revoked_at TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
