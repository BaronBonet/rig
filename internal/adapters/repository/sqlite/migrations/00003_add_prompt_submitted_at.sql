-- +goose Up
ALTER TABLE task_hook_sessions ADD COLUMN last_prompt_submitted_at text NOT NULL DEFAULT '';

-- +goose Down
-- SQLite does not support drop column before 3.35.0; left intentionally blank.
