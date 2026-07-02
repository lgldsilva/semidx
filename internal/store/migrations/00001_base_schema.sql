-- +goose Up
-- Base schema: the vector extension plus the static projects/files tables.
-- The per-dimension chunks_<dims> tables are created at index time by
-- EnsureChunksTable (their column type depends on the model's dimension).
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS projects (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL,
    model TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'indexing',
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS files (
    id SERIAL PRIMARY KEY,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    hash TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    indexed_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(project_id, path)
);

-- +goose Down
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS projects;
