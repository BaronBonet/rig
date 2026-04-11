-- name: GetObserverSummaryByTaskID :one
select
  task_id, display_status, display_activity, process_alive,
  last_runtime_observed_at
from task_observer_summaries
where task_id = sqlc.arg(task_id)
limit 1;

-- name: ListAllObserverSummaries :many
select
  task_id, display_status, display_activity, process_alive,
  last_runtime_observed_at
from task_observer_summaries
;

-- name: ListObserverSummariesByTaskIDs :many
select
  task_id, display_status, display_activity, process_alive,
  last_runtime_observed_at
from task_observer_summaries
where task_id in (sqlc.slice(task_ids));

-- name: UpsertObserverSummary :exec
insert into task_observer_summaries (
  task_id, display_status, display_activity, process_alive,
  last_runtime_observed_at, updated_at
) values (?, ?, ?, ?, ?, ?)
on conflict(task_id) do update set
  display_status = excluded.display_status,
  display_activity = excluded.display_activity,
  process_alive = excluded.process_alive,
  last_runtime_observed_at = excluded.last_runtime_observed_at,
  updated_at = excluded.updated_at;
