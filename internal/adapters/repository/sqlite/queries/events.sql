-- name: AppendEvent :exec
insert into events (task_id, event_type, payload, created_at)
values (?, ?, ?, ?);
