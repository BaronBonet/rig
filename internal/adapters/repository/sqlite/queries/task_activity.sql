-- name: InsertTaskActivity :exec
insert into task_activity (
  task_id, agent_session_id, turn_id, event_name, role, text, observed_at
) values (?, ?, ?, ?, ?, ?, ?);

-- name: ListTaskActivityByTaskID :many
select
  id, task_id, turn_id, event_name, role, text, observed_at, agent_session_id
from task_activity
where task_id = ?
order by observed_at asc, id asc;

-- name: ListTaskActivityByTaskIDLimitedDesc :many
select
  id, task_id, turn_id, event_name, role, text, observed_at, agent_session_id
from task_activity
where task_id = ?
order by observed_at desc, id desc
limit ?;

-- The newest prompt of each of a task's open agent sessions, found per open
-- agent session through the activity agent session index, which a task with
-- much history needs: it runs on every hook that changes the task's status.
-- Times are RFC 3339 text without trailing zeros, so text order is not time
-- order within one second; julianday compares them as times, and ties go to
-- the newest row.
-- name: ListLatestAgentSessionPrompts :many
select
  a.id, a.task_id, a.turn_id, a.event_name, a.role, a.text, a.observed_at, a.agent_session_id
from agent_sessions s
join task_activity a on a.id = (
  select b.id
  from task_activity b
  where b.agent_session_id = s.id and b.agent_session_id != ''
    and b.task_id = s.task_id and b.role = 'user'
  order by julianday(b.observed_at) desc, b.id desc
  limit 1
)
where s.task_id = ? and s.ended_at = '';
