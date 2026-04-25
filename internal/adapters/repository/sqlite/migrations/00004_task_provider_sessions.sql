-- +goose Up

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
