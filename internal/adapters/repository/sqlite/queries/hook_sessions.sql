-- name: GetHookSessionSummaryByTaskID :one
select
  task_id, session_id, model, cwd, transcript_path, start_source,
  current_turn_id, last_event_name, runtime_phase, started_at,
  last_activity_at, last_stop_at, last_prompt_preview,
  last_command_preview, last_command_result_preview,
  last_assistant_message, command_count
from task_hook_sessions
where task_id = sqlc.arg(task_id)
limit 1;

-- name: ListHookSessionSummaries :many
select
  task_id, session_id, model, cwd, transcript_path, start_source,
  current_turn_id, last_event_name, runtime_phase, started_at,
  last_activity_at, last_stop_at, last_prompt_preview,
  last_command_preview, last_command_result_preview,
  last_assistant_message, command_count
from task_hook_sessions
order by task_id asc;

-- name: UpsertHookSessionSummary :exec
insert into task_hook_sessions (
  task_id, session_id, model, cwd, transcript_path, start_source,
  current_turn_id, last_event_name, runtime_phase, started_at,
  last_activity_at, last_stop_at, last_prompt_preview,
  last_command_preview, last_command_result_preview,
  last_assistant_message, command_count, updated_at
) values (
  ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
on conflict(task_id) do update set
  session_id = excluded.session_id,
  model = excluded.model,
  cwd = excluded.cwd,
  transcript_path = excluded.transcript_path,
  start_source = excluded.start_source,
  current_turn_id = excluded.current_turn_id,
  last_event_name = excluded.last_event_name,
  runtime_phase = excluded.runtime_phase,
  started_at = excluded.started_at,
  last_activity_at = excluded.last_activity_at,
  last_stop_at = excluded.last_stop_at,
  last_prompt_preview = excluded.last_prompt_preview,
  last_command_preview = excluded.last_command_preview,
  last_command_result_preview = excluded.last_command_result_preview,
  last_assistant_message = excluded.last_assistant_message,
  command_count = excluded.command_count,
  updated_at = excluded.updated_at;
