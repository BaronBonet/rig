-- +goose Up
alter table task_hook_sessions add column last_prompt_submitted_at text not null default '';

-- +goose Down
-- SQLite does not support drop column before 3.35.0; left intentionally blank.
