-- +goose Up

alter table tasks add column workspace_kind text not null default 'worktree';

-- +goose Down

alter table tasks drop column workspace_kind;
