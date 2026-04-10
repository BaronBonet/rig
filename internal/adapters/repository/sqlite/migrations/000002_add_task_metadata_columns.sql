alter table tasks add column repo_name text not null default '';
alter table tasks add column agent_window_name text not null default 'agent';
alter table tasks add column editor_window_name text not null default 'editor';
alter table tasks add column agent_window_exists integer not null default 0;
alter table tasks add column editor_window_exists integer not null default 0;
