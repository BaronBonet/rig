-- A task's Runtime status is derived from its agent sessions, which carry
-- their own hook-driven status, so the per-task status row is gone.

-- +goose Up

drop table if exists task_status;

-- +goose Down

create table if not exists task_status (
  task_id text primary key,
  provider text not null,
  phase text not null,
  raw_event_name text not null,
  observed_at text not null,
  background_subagents integer not null default 0,
  background_shells integer not null default 0,
  background_monitors integer not null default 0,
  background_workflows integer not null default 0,
  background_other integer not null default 0,
  foreign key(task_id) references tasks(id) on delete cascade
);
