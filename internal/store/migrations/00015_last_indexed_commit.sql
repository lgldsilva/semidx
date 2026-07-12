-- +goose Up
-- +goose StatementBegin
ALTER TABLE projects ADD COLUMN IF NOT EXISTS last_indexed_commit TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE projects DROP COLUMN IF EXISTS last_indexed_commit;
-- +goose StatementEnd
