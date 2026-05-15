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
  updated_at text not null,
  creation_status text not null default 'ready',
  creation_step text not null default '',
  creation_error text not null default ''
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

create table if not exists task_resume_metadata (
  task_id text primary key,
  provider text not null,
  session_id text not null,
  observed_at text not null,
  foreign key(task_id) references tasks(id) on delete cascade
);

create table if not exists task_activity (
  id integer primary key autoincrement,
  task_id text not null,
  turn_id text not null,
  event_name text not null,
  role text not null,
  text text not null,
  observed_at text not null,
  foreign key(task_id) references tasks(id) on delete cascade
);

create index if not exists idx_task_activity_task_observed
  on task_activity(task_id, observed_at desc, id desc);

create table if not exists task_provider_sessions (
  id integer primary key autoincrement,
  task_id text not null,
  provider text not null,
  provider_session_id text not null,
  transcript_path text not null default '',
  start_source text not null default '',
  model text not null default '',
  cwd text not null default '',
  first_observed_at text not null,
  last_observed_at text not null,
  last_event_name text not null default '',
  foreign key(task_id) references tasks(id) on delete cascade,
  unique(task_id, provider, provider_session_id, transcript_path)
);

create index if not exists idx_task_provider_sessions_task_last_observed
  on task_provider_sessions(task_id, last_observed_at desc, id desc);

create index if not exists idx_task_provider_sessions_task_provider_session
  on task_provider_sessions(task_id, provider, provider_session_id);

-- +goose Down

drop table if exists task_provider_sessions;
drop table if exists task_activity;
drop table if exists task_resume_metadata;
drop table if exists task_status;
drop table if exists tasks;
