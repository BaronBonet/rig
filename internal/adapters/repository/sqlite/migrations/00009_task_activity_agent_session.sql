-- Activity from a hook event Rig placed in an agent session records that
-- agent session, so a task's status can carry each agent's latest prompt.
-- Activity recovered from transcripts, or from events Rig could not place,
-- has none.

-- +goose Up

alter table task_activity add column agent_session_id text not null default '';

create index if not exists idx_task_activity_agent_session
  on task_activity(agent_session_id, role)
  where agent_session_id != '';

-- +goose Down

drop index if exists idx_task_activity_agent_session;
alter table task_activity drop column agent_session_id;
