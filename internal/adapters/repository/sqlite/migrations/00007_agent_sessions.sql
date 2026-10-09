-- +goose Up

create table if not exists agent_sessions (
  id text primary key,
  task_id text not null,
  provider text not null,
  tmux_socket_path text not null default '',
  tmux_server_pid integer not null default 0,
  tmux_pane text not null default '',
  pane_trusted integer not null default 0,
  provider_session_id text not null default '',
  started_at text not null,
  ended_at text not null default '',
  status_phase text not null default '',
  status_raw_event_name text not null default '',
  status_observed_at text not null default '',
  background_subagents integer not null default 0,
  background_shells integer not null default 0,
  background_monitors integer not null default 0,
  background_workflows integer not null default 0,
  background_other integer not null default 0,
  foreign key(task_id) references tasks(id) on delete cascade
);

-- A pane runs one agent at a time. Agent sessions without a pane, which
-- reconnect restores, never conflict.
create unique index if not exists idx_agent_sessions_open_pane
  on agent_sessions(tmux_socket_path, tmux_server_pid, tmux_pane)
  where ended_at = '' and tmux_pane != '';

create index if not exists idx_agent_sessions_task_open
  on agent_sessions(task_id, ended_at, started_at);

-- +goose Down

drop index if exists idx_agent_sessions_task_open;
drop index if exists idx_agent_sessions_open_pane;
drop table if exists agent_sessions;
