-- +goose Up

alter table tasks add column parent_id text not null default '';
alter table tasks add column tmux_window text not null default '';

-- +goose Down

alter table tasks drop column tmux_window;
alter table tasks drop column parent_id;
