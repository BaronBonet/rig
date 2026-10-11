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
  start_source = case
    when task_provider_sessions.start_source = '' then excluded.start_source
    else task_provider_sessions.start_source
  end,
  -- The first model observed stays: an event without one never clears it, and
  -- a SessionStart can report the model a resumed conversation started on.
  -- Only a model switch replaces it, with SwitchTaskProviderSessionModel.
  model = case
    when task_provider_sessions.model = '' then excluded.model
    else task_provider_sessions.model
  end,
  cwd = case
    when task_provider_sessions.cwd = '' then excluded.cwd
    else task_provider_sessions.cwd
  end,
  first_observed_at = task_provider_sessions.first_observed_at,
  last_observed_at = case
    when julianday(excluded.last_observed_at) > julianday(task_provider_sessions.last_observed_at) then excluded.last_observed_at
    else task_provider_sessions.last_observed_at
  end,
  last_event_name = case
    when julianday(excluded.last_observed_at) >= julianday(task_provider_sessions.last_observed_at) then excluded.last_event_name
    else task_provider_sessions.last_event_name
  end;

-- name: SwitchTaskProviderSessionModel :exec
-- A model switch replaces a Provider session's model, unless the Provider
-- session has seen a newer event. It runs after the switch's upsert, which
-- moved last_observed_at to the newest event seen, so a tie means no newer
-- event was seen.
update task_provider_sessions
set model = sqlc.arg(model)
where task_id = sqlc.arg(task_id)
  and provider = sqlc.arg(provider)
  and provider_session_id = sqlc.arg(provider_session_id)
  and transcript_path = sqlc.arg(transcript_path)
  and julianday(last_observed_at) <= julianday(cast(sqlc.arg(observed_at) as text));

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
