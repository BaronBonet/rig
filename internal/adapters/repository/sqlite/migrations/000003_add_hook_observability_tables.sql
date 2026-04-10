create table if not exists task_hook_events (
  id integer primary key autoincrement,
  task_id text not null,
  session_id text not null default '',
  turn_id text not null default '',
  event_name text not null,
  occurred_at text not null,
  raw_payload_json text not null default '',
  last_assistant_message text not null default '',
  prompt_preview text not null default '',
  command_preview text not null default '',
  command_result_preview text not null default '',
  tool_use_id text not null default ''
);

create table if not exists task_hook_sessions (
  task_id text primary key,
  session_id text not null default '',
  model text not null default '',
  cwd text not null default '',
  transcript_path text not null default '',
  start_source text not null default '',
  current_turn_id text not null default '',
  last_event_name text not null default '',
  runtime_phase text not null default '',
  started_at text not null default '',
  last_activity_at text not null default '',
  last_stop_at text not null default '',
  last_prompt_preview text not null default '',
  last_command_preview text not null default '',
  last_command_result_preview text not null default '',
  last_assistant_message text not null default '',
  command_count integer not null default 0,
  updated_at text not null default ''
);

create table if not exists task_observer_summaries (
  task_id text primary key,
  display_status text not null default '',
  display_activity text not null default '',
  process_alive integer not null default 0,
  last_runtime_observed_at text not null default '',
  updated_at text not null default ''
);

create index if not exists idx_task_hook_events_task_occurred_at on task_hook_events(task_id, occurred_at desc, id desc);
create index if not exists idx_task_hook_sessions_session_id on task_hook_sessions(session_id);
