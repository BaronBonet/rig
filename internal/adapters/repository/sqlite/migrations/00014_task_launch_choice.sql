-- A task records the model and reasoning level chosen for its launch
-- provider at creation. Empty means no choice, so Rig passes the provider CLI
-- no flag and its own default applies.

-- +goose Up

alter table tasks add column model text not null default '';
alter table tasks add column reasoning_level text not null default '';

-- +goose Down

alter table tasks drop column reasoning_level;
alter table tasks drop column model;
