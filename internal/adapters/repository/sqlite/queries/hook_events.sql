-- name: InsertHookEvent :exec
insert into task_hook_events (
  task_id, session_id, turn_id, event_name, occurred_at,
  raw_payload_json, last_assistant_message, prompt_preview,
  command_preview, command_result_preview, tool_use_id
) values (
  ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: ListHookEventsByTaskID :many
select
  id, task_id, session_id, turn_id, event_name, occurred_at,
  raw_payload_json, last_assistant_message, prompt_preview,
  command_preview, command_result_preview, tool_use_id
from task_hook_events
where task_id = sqlc.arg(task_id)
order by occurred_at desc, id desc;

-- name: ListHookEventsByTaskIDLimited :many
select
  id, task_id, session_id, turn_id, event_name, occurred_at,
  raw_payload_json, last_assistant_message, prompt_preview,
  command_preview, command_result_preview, tool_use_id
from task_hook_events
where task_id = sqlc.arg(task_id)
order by occurred_at desc, id desc
limit sqlc.arg(limit);
