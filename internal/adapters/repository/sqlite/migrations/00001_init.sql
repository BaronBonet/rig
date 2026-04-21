-- +goose Up

create table if not exists tasks (
  id text primary key,
  slug text not null,
  prompt text not null,
  display_name text not null,
  repo_root text not null,
  repo_name text not null,
  branch_name text not null,
  worktree_path text not null,
  tmux_session text not null,
  provider text not null,
  created_at text not null,
  updated_at text not null
);

create index if not exists idx_tasks_worktree_path_created_at
  on tasks(worktree_path, created_at desc);

create table if not exists task_status (
  task_id text primary key,
  provider text not null,
  phase text not null,
  raw_event_name text not null,
  observed_at text not null,
  foreign key(task_id) references tasks(id) on delete cascade
);

-- +goose Down

