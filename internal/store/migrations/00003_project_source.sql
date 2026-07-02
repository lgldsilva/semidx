-- +goose Up
-- Where a project's content comes from: "push" (clients upload chunks),
-- "git" (the server clones/pulls git_url), or "path" (a local index run).
ALTER TABLE projects ADD COLUMN IF NOT EXISTS source_type TEXT NOT NULL DEFAULT 'push';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS git_url TEXT;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS branch TEXT;

-- +goose Down
ALTER TABLE projects DROP COLUMN IF EXISTS branch;
ALTER TABLE projects DROP COLUMN IF EXISTS git_url;
ALTER TABLE projects DROP COLUMN IF EXISTS source_type;
