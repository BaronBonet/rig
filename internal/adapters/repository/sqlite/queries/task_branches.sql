-- name: ListTaskBranches :many
select branch_name
from task_branches
where task_id = ?
order by branch_name;

-- name: RecordTaskBranch :exec
insert into task_branches (task_id, branch_name) values (?, ?)
on conflict(task_id, branch_name) do nothing;
