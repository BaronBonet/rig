-- +goose Up

alter table tasks add column provider_env text not null default '';

-- +goose Down

alter table tasks drop column provider_env;
