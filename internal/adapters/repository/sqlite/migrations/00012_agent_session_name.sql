-- An agent session keeps a short name, derived like a task's name from the
-- first prompt of a conversation, and the Provider session it was derived
-- from, so a new conversation, as after /clear, renames the agent.

-- +goose Up

alter table agent_sessions add column name text not null default '';
alter table agent_sessions add column named_provider_session_id text not null default '';

-- +goose Down

alter table agent_sessions drop column named_provider_session_id;
alter table agent_sessions drop column name;
