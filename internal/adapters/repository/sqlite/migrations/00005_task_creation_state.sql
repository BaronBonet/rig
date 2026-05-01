-- +goose Up

alter table tasks add column creation_status text not null default 'ready';
alter table tasks add column creation_step text not null default '';
alter table tasks add column creation_error text not null default '';

-- +goose Down

