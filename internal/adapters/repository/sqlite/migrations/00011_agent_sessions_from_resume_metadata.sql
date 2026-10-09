-- Reconnect resumes each agent session's own conversation, so the per-task
-- resume metadata goes. A task's last conversation becomes an open agent
-- session without a pane, so a task whose Session is already gone still
-- reconnects to it. A task that has agent sessions keeps just those: they
-- were recorded since agent sessions existed, and are better evidence.

-- +goose Up

-- A migrated agent session takes its task's ID: unique and, like the IDs Rig
-- generates, a creation time in nanoseconds, before any agent session Rig
-- opens from now on. A task whose ID an agent session already holds, and
-- resume metadata whose task is gone, are left out rather than failing the
-- migration.
insert into agent_sessions (id, task_id, provider, provider_session_id, started_at)
select
  m.task_id,
  m.task_id,
  m.provider,
  m.session_id,
  case when m.observed_at != '' then m.observed_at else strftime('%Y-%m-%dT%H:%M:%SZ', 'now') end
from task_resume_metadata m
where trim(m.provider) != ''
  and trim(m.session_id) != ''
  and exists (select 1 from tasks t where t.id = m.task_id)
  and not exists (
    select 1 from agent_sessions s
    where s.task_id = m.task_id or s.id = m.task_id
  );

drop table if exists task_resume_metadata;

-- +goose Down

create table if not exists task_resume_metadata (
  task_id text primary key,
  provider text not null,
  session_id text not null,
  observed_at text not null,
  foreign key(task_id) references tasks(id) on delete cascade
);

-- Each task's most recently started agent session with a conversation
-- becomes its resume metadata again.
insert into task_resume_metadata (task_id, provider, session_id, observed_at)
select s.task_id, s.provider, s.provider_session_id, s.started_at
from agent_sessions s
where exists (select 1 from tasks t where t.id = s.task_id)
  and s.id = (
    select l.id from agent_sessions l
    where l.task_id = s.task_id and l.provider_session_id != ''
    order by julianday(l.started_at) desc, l.id desc
    limit 1
  );
