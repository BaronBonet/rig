-- An agent session Rig launched records when, so a status cycle gives the
-- agent a moment to start in its pane before its first hook event arrives.

-- +goose Up

alter table agent_sessions add column launched_at text not null default '';

-- +goose Down

alter table agent_sessions drop column launched_at;
