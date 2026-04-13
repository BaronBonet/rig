-- +goose Up
CREATE INDEX idx_tasks_repo_root ON tasks(repo_root);

-- +goose Down
DROP INDEX IF EXISTS idx_tasks_repo_root;
