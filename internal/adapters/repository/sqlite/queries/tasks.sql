-- name: CreateTask :exec
insert into tasks (
  id, prompt, display_name, slug, repo_root, repo_name, base_branch, branch_name,
  worktree_path, tmux_session, agent_window_name, editor_window_name,
  provider, status, worktree_exists, branch_exists, session_exists,
  agent_window_exists, editor_window_exists,
  created_at, updated_at, last_reconciled_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateTask :exec
update tasks set
  prompt = ?,
  display_name = ?,
  slug = ?,
  repo_root = ?,
  repo_name = ?,
  base_branch = ?,
  branch_name = ?,
  worktree_path = ?,
  tmux_session = ?,
  agent_window_name = ?,
  editor_window_name = ?,
  provider = ?,
  status = ?,
  worktree_exists = ?,
  branch_exists = ?,
  session_exists = ?,
  agent_window_exists = ?,
  editor_window_exists = ?,
  created_at = ?,
  updated_at = ?,
  last_reconciled_at = ?
where id = ?;

-- name: GetTaskByIDOrSlug :one
select
  id, prompt, display_name, slug, repo_root, repo_name, base_branch, branch_name,
  worktree_path, tmux_session, agent_window_name, editor_window_name,
  provider, status, worktree_exists, branch_exists, session_exists,
  agent_window_exists, editor_window_exists,
  created_at, updated_at, last_reconciled_at
from tasks
where id = sqlc.arg(id_or_slug) or slug = sqlc.arg(id_or_slug)
limit 1;

-- name: ListTasks :many
select
  id, prompt, display_name, slug, repo_root, repo_name, base_branch, branch_name,
  worktree_path, tmux_session, agent_window_name, editor_window_name,
  provider, status, worktree_exists, branch_exists, session_exists,
  agent_window_exists, editor_window_exists,
  created_at, updated_at, last_reconciled_at
from tasks
order by created_at asc;

-- name: ListTasksByRepo :many
select
  id, prompt, display_name, slug, repo_root, repo_name, base_branch, branch_name,
  worktree_path, tmux_session, agent_window_name, editor_window_name,
  provider, status, worktree_exists, branch_exists, session_exists,
  agent_window_exists, editor_window_exists,
  created_at, updated_at, last_reconciled_at
from tasks
where repo_root = ?
order by created_at asc;
