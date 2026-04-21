-- name: UpsertTaskStatus :exec
insert into task_status (
  task_id, provider, phase, raw_event_name, observed_at
) values (?, ?, ?, ?, ?)
on conflict(task_id) do update set
  provider = excluded.provider,
  phase = excluded.phase,
  raw_event_name = excluded.raw_event_name,
  observed_at = excluded.observed_at;

-- name: LatestTaskStatus :one
select
  task_id, provider, phase, raw_event_name, observed_at
from task_status
where task_id = ?;
