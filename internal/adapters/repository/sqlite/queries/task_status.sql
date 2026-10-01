-- name: UpsertTaskStatus :exec
insert into task_status (
  task_id, provider, phase, raw_event_name, observed_at,
  background_subagents, background_shells, background_monitors, background_workflows, background_other
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(task_id) do update set
  provider = excluded.provider,
  phase = excluded.phase,
  raw_event_name = excluded.raw_event_name,
  observed_at = excluded.observed_at,
  background_subagents = excluded.background_subagents,
  background_shells = excluded.background_shells,
  background_monitors = excluded.background_monitors,
  background_workflows = excluded.background_workflows,
  background_other = excluded.background_other;

-- name: LatestTaskStatus :one
select
  task_id, provider, phase, raw_event_name, observed_at,
  background_subagents, background_shells, background_monitors, background_workflows, background_other
from task_status
where task_id = ?;
