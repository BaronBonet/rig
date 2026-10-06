-- +goose Up

alter table tasks add column shelved_at text not null default '';

-- +goose Down

alter table tasks drop column shelved_at;
