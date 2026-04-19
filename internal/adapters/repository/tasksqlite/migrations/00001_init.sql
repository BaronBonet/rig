-- +goose Up

create table if not exists tasks (
  id text primary key,
  prompt text not null,
  display_name text not null,
  repo_root text not null,
  repo_name text not null,
  branch_name text not null,
  worktree_path text not null,
  tmux_session text not null,
  provider text not null,
  status text not null,
  created_at text not null,
  updated_at text not null
);

create index if not exists idx_tasks_worktree_path_created_at
  on tasks(worktree_path, created_at desc);

-- +goose Down

-- Intentionally left blank for the goose baseline.
