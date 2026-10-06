-- name: CreateTask :exec
insert into tasks (
  id, slug, prompt, display_name, repo_root, repo_name, branch_name,
  worktree_path, tmux_session, provider, creation_status, creation_step,
  creation_error, created_at, updated_at, workspace_kind, provider_env
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteTask :exec
delete from tasks
where id = ?;

-- name: UpdateTask :exec
update tasks set
  slug = ?,
  prompt = ?,
  display_name = ?,
  repo_root = ?,
  repo_name = ?,
  branch_name = ?,
  worktree_path = ?,
  tmux_session = ?,
  provider = ?,
  creation_status = ?,
  creation_step = ?,
  creation_error = ?,
  created_at = ?,
  updated_at = ?,
  workspace_kind = ?,
  provider_env = ?
where id = ?;

-- name: ListTasks :many
select
  id, slug, prompt, display_name, repo_root, repo_name, branch_name,
  worktree_path, tmux_session, provider, creation_status, creation_step,
  creation_error, created_at, updated_at, workspace_kind, provider_env
from tasks
order by created_at asc;
