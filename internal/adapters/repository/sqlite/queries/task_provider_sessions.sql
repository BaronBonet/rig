-- name: UpsertTaskProviderSession :exec
insert into task_provider_sessions (
  task_id,
  provider,
  provider_session_id,
  transcript_path,
  start_source,
  model,
  cwd,
  first_observed_at,
  last_observed_at,
  last_event_name
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(task_id, provider, provider_session_id, transcript_path) do update set
  start_source = excluded.start_source,
  model = excluded.model,
  cwd = excluded.cwd,
  first_observed_at = task_provider_sessions.first_observed_at,
  last_observed_at = excluded.last_observed_at,
  last_event_name = excluded.last_event_name;

-- name: ListTaskProviderSessions :many
select
  task_id,
  provider,
  provider_session_id,
  transcript_path,
  start_source,
  model,
  cwd,
  first_observed_at,
  last_observed_at,
  last_event_name
from task_provider_sessions
where task_id = ?
order by first_observed_at, id;
