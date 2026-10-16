-- An agent session keeps when Rig last saw its pane on screen, so a turn end
-- newer than that is unseen. Open agent sessions start seen, so an upgrade
-- does not highlight every agent that already needs input.

-- +goose Up

alter table agent_sessions add column seen_at text not null default '';
update agent_sessions set seen_at = status_observed_at where ended_at = '';

-- +goose Down

alter table agent_sessions drop column seen_at;
