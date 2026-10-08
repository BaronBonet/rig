-- name: UpsertTaskWorktree :exec
insert into task_worktrees (
  task_id,
  worktree_path,
  repo_name,
  branch,
  last_edit_at,
  edit_count
) values (?, ?, ?, ?, ?, ?)
on conflict(task_id, worktree_path) do update set
  repo_name = excluded.repo_name,
  branch = excluded.branch,
  last_edit_at = excluded.last_edit_at,
  edit_count = excluded.edit_count;

-- name: ListTaskWorktrees :many
select
  task_id,
  worktree_path,
  repo_name,
  branch,
  last_edit_at,
  edit_count
from task_worktrees
where task_id = ?
order by last_edit_at desc, worktree_path;
