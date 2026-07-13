-- +goose Up
-- Persistent chat conversations (Fase 12): a user may keep multiple chats, each
-- bound to one project or global (project = ''). Messages are stored per turn
-- with optional citation JSON so a conversation can be reopened with its sources.
CREATE TABLE IF NOT EXISTS conversations (
    id SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project TEXT NOT NULL DEFAULT '', -- '' = global (all projects)
    title TEXT NOT NULL DEFAULT 'New chat',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations (user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS conversation_messages (
    id SERIAL PRIMARY KEY,
    conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL, -- user | assistant
    content TEXT NOT NULL,
    sources_json TEXT NOT NULL DEFAULT '', -- citation JSON for assistant turns
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_conv_messages ON conversation_messages (conversation_id, id);

-- +goose Down
DROP TABLE IF EXISTS conversation_messages;
DROP TABLE IF EXISTS conversations;
