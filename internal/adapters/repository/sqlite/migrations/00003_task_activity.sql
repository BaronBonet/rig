-- +goose Up

create table if not exists task_activity (
  id integer primary key autoincrement,
  task_id text not null,
  turn_id text not null,
  event_name text not null,
  role text not null,
  text text not null,
  observed_at text not null,
  foreign key(task_id) references tasks(id) on delete cascade
);

create index if not exists idx_task_activity_task_observed
  on task_activity(task_id, observed_at desc, id desc);

-- +goose Down

drop table if exists task_activity;
