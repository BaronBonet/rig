-- +goose Up

create table if not exists task_worktrees (
  task_id text not null,
  worktree_path text not null,
  repo_name text not null default '',
  branch text not null default '',
  last_edit_at text not null,
  edit_count integer not null default 0,
  primary key(task_id, worktree_path),
  foreign key(task_id) references tasks(id) on delete cascade
);

-- +goose Down

drop table if exists task_worktrees;
