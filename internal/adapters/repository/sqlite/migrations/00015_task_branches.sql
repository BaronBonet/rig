-- A task's branches are the branches its worktree has had checked out, found
-- in the worktree's reflog. They are recorded because git expires old reflog
-- entries, while a branch's merged pull request still belongs to the task.

-- +goose Up

create table if not exists task_branches (
  task_id text not null,
  branch_name text not null,
  primary key (task_id, branch_name),
  foreign key(task_id) references tasks(id) on delete cascade
);

-- +goose Down

drop table if exists task_branches;
