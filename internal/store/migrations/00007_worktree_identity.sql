-- +goose Up
-- F11: a git project is one logical index keyed by repo identity (shared by all
-- worktrees/clones), files are content-addressed so divergent versions of the
-- same path coexist, and a per-worktree manifest records what each checkout has.

-- Stable project identity (normalized remote / git-common-dir, or an absolute
-- path for document folders). Backfill existing rows from the name.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS identity TEXT;
UPDATE projects SET identity = name WHERE identity IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS projects_identity_idx ON projects (identity);

-- Content-addressed files: replace UNIQUE(project_id, path) with
-- UNIQUE(project_id, path, hash) so two worktrees can hold different content at
-- the same relative path without clobbering each other.
ALTER TABLE files DROP CONSTRAINT IF EXISTS files_project_id_path_key;
CREATE UNIQUE INDEX IF NOT EXISTS files_project_path_hash_idx ON files (project_id, path, hash);

-- Per-worktree manifest: the (path -> hash) set a given worktree currently has
-- checked out. Search filters chunks through this so each worktree sees its own
-- version; indexing rewrites a worktree's rows on each run.
CREATE TABLE IF NOT EXISTS worktree_files (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    worktree   TEXT NOT NULL,
    path       TEXT NOT NULL,
    hash       TEXT NOT NULL,
    PRIMARY KEY (project_id, worktree, path)
);
CREATE INDEX IF NOT EXISTS worktree_files_lookup ON worktree_files (project_id, worktree);

-- +goose Down
DROP TABLE IF EXISTS worktree_files;
DROP INDEX IF EXISTS files_project_path_hash_idx;
CREATE UNIQUE INDEX IF NOT EXISTS files_project_id_path_key ON files (project_id, path);
DROP INDEX IF EXISTS projects_identity_idx;
ALTER TABLE projects DROP COLUMN IF EXISTS identity;
