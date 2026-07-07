-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS file_dependencies (
    project_id  INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_file TEXT    NOT NULL,
    target_file TEXT    NOT NULL,
    PRIMARY KEY (project_id, source_file, target_file)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS file_dependencies;
-- +goose StatementEnd
