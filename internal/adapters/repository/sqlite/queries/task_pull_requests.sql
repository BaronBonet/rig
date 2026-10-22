-- name: GetTaskPullRequests :one
select pull_requests
from task_pull_requests
where task_id = ?;

-- name: SaveTaskPullRequests :exec
insert into task_pull_requests (task_id, pull_requests) values (?, ?)
on conflict(task_id) do update set pull_requests = excluded.pull_requests;
