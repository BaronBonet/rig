-- Versions 2-5 belonged to migrations squashed into 00001_init.sql. Databases
-- created before the squash still record them as applied, so new migrations
-- must be numbered above 5.

-- +goose Up

alter table task_status add column background_subagents integer not null default 0;
alter table task_status add column background_shells integer not null default 0;
alter table task_status add column background_monitors integer not null default 0;
alter table task_status add column background_workflows integer not null default 0;
alter table task_status add column background_other integer not null default 0;

-- +goose Down

alter table task_status drop column background_other;
alter table task_status drop column background_workflows;
alter table task_status drop column background_monitors;
alter table task_status drop column background_shells;
alter table task_status drop column background_subagents;
