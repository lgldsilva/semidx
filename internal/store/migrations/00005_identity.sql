-- +goose Up
-- Identity model: users authenticate to the web UI with a password (argon2id);
-- browser sessions live server-side in web_sessions; API tokens can belong to a
-- user (the bootstrap token has none, so the column is nullable).
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'member', -- 'admin' | 'member'
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    disabled_at TIMESTAMP
);

-- Only the SHA-256 hash of a session token is stored; the browser cookie holds
-- the plaintext, so a database read never exposes a usable session.
CREATE TABLE IF NOT EXISTS web_sessions (
    token_hash TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS web_sessions_user_idx ON web_sessions (user_id);
CREATE INDEX IF NOT EXISTS web_sessions_expires_idx ON web_sessions (expires_at);

ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS user_id INTEGER REFERENCES users(id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN IF EXISTS user_id;
DROP TABLE IF EXISTS web_sessions;
DROP TABLE IF EXISTS users;
