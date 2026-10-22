-- A task keeps the pull requests and unshipped work it was last found to
-- have, so the task list shows them at once while gh is asked again.

-- +goose Up

create table if not exists task_pull_requests (
  task_id text primary key,
  pull_requests text not null,
  foreign key(task_id) references tasks(id) on delete cascade
);

-- +goose Down

drop table if exists task_pull_requests;
