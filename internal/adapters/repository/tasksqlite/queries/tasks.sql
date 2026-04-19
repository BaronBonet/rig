-- name: CreateTask :exec
insert into tasks (
  id, prompt, display_name, repo_root, repo_name, branch_name,
  worktree_path, tmux_session, provider, status, created_at, updated_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateTask :exec
update tasks set
  prompt = ?,
  display_name = ?,
  repo_root = ?,
  repo_name = ?,
  branch_name = ?,
  worktree_path = ?,
  tmux_session = ?,
  provider = ?,
  status = ?,
  created_at = ?,
  updated_at = ?
where id = ?;

-- name: GetTaskByID :one
select
  id, prompt, display_name, repo_root, repo_name, branch_name,
  worktree_path, tmux_session, provider, status, created_at, updated_at
from tasks
where id = ?;

-- name: ListTasks :many
select
  id, prompt, display_name, repo_root, repo_name, branch_name,
  worktree_path, tmux_session, provider, status, created_at, updated_at
from tasks
order by created_at asc;
