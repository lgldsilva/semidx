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

-- chunks_* tables are created dynamically by the application
-- based on the embedding model's dimension (e.g. chunks_768, chunks_1024).
