-- name: UpsertTaskResumeMetadata :exec
insert into task_resume_metadata (
  task_id, provider, session_id, observed_at
) values (?, ?, ?, ?)
on conflict(task_id) do update set
  provider = excluded.provider,
  session_id = excluded.session_id,
  observed_at = excluded.observed_at;

-- name: LatestTaskResumeMetadata :one
select
  task_id, provider, session_id, observed_at
from task_resume_metadata
where task_id = ?;
