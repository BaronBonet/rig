-- +goose Up

alter table tasks add column session_title text not null default '';

-- +goose Down

alter table tasks drop column session_title;
