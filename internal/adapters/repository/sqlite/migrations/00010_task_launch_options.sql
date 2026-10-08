-- +goose Up

alter table tasks add column model text not null default '';
alter table tasks add column effort text not null default '';

-- +goose Down

alter table tasks drop column effort;
alter table tasks drop column model;
