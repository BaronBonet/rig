-- name: InsertTaskActivity :exec
insert into task_activity (
  task_id, turn_id, event_name, role, text, observed_at
) values (?, ?, ?, ?, ?, ?);

-- name: ListTaskActivityByTaskID :many
select
  id, task_id, turn_id, event_name, role, text, observed_at
from task_activity
where task_id = ?
order by observed_at asc, id asc;

-- name: ListTaskActivityByTaskIDLimitedDesc :many
select
  id, task_id, turn_id, event_name, role, text, observed_at
from task_activity
where task_id = ?
order by observed_at desc, id desc
limit ?;
