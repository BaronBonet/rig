-- name: GetTaskIDByID :one
select id
from tasks
where id = sqlc.arg(id)
limit 1;

-- name: GetTaskIDByWorktreePath :one
select id
from tasks
where worktree_path = sqlc.arg(worktree_path)
order by created_at desc
limit 1;

-- name: GetTaskIDBySessionID :one
select task_id
from task_hook_sessions
where session_id = sqlc.arg(session_id)
limit 1;
