-- +goose Up
-- F14: a project's stable key is its `identity` (git identity or "path:<abs>"),
-- unique since 00007. The `name` is just a display basename and MUST be allowed
-- to repeat — otherwise two folders that share a basename (e.g. two ".../backend"
-- document folders, or two repos named "app") collide on insert. Drop the
-- UNIQUE(name) constraint from the base schema; identity uniqueness stays.
ALTER TABLE projects DROP CONSTRAINT IF EXISTS projects_name_key;

-- +goose Down
-- Re-adding the constraint would fail if duplicate names now exist; recreate it
-- only when the data still permits (best-effort, non-fatal in practice).
ALTER TABLE projects ADD CONSTRAINT projects_name_key UNIQUE (name);
