-- A task created from inside another task's worktree records that task as
-- its parent. Deleting the parent leaves its children in place, unlinked.

-- +goose Up

alter table tasks add column parent_task_id text references tasks(id) on delete set null;

-- Deleting a task finds its children by this index to unlink them.
create index if not exists idx_tasks_parent_task_id on tasks(parent_task_id);

-- +goose Down

drop index if exists idx_tasks_parent_task_id;

alter table tasks drop column parent_task_id;
