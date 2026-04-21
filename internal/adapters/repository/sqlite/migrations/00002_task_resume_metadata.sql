-- +goose Up

create table if not exists task_resume_metadata (
  task_id text primary key,
  provider text not null,
  session_id text not null,
  observed_at text not null,
  foreign key(task_id) references tasks(id) on delete cascade
);

-- +goose Down

drop table if exists task_resume_metadata;
