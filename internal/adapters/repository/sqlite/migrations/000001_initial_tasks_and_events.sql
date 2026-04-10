create table if not exists tasks (
  id text primary key,
  prompt text not null,
  display_name text not null,
  slug text not null unique,
  repo_root text not null,
  base_branch text not null,
  branch_name text not null,
  worktree_path text not null,
  tmux_session text not null,
  provider text not null,
  status text not null,
  worktree_exists integer not null,
  branch_exists integer not null,
  session_exists integer not null,
  last_error text not null default '',
  created_at text not null,
  updated_at text not null,
  last_reconciled_at text not null default ''
);

create table if not exists events (
  id integer primary key autoincrement,
  task_id text not null,
  event_type text not null,
  payload text not null,
  created_at text not null
);
