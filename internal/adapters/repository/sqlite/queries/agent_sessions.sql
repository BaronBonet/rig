-- name: CreateAgentSession :exec
insert into agent_sessions (
  id,
  task_id,
  provider,
  tmux_socket_path,
  tmux_server_pid,
  tmux_pane,
  pane_trusted,
  provider_session_id,
  started_at,
  ended_at,
  status_phase,
  status_raw_event_name,
  status_observed_at,
  background_subagents,
  background_shells,
  background_monitors,
  background_workflows,
  background_other,
  launched_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: OpenAgentSessionOnPane :one
select
  id,
  task_id,
  provider,
  tmux_socket_path,
  tmux_server_pid,
  tmux_pane,
  pane_trusted,
  provider_session_id,
  started_at,
  ended_at,
  status_phase,
  status_raw_event_name,
  status_observed_at,
  background_subagents,
  background_shells,
  background_monitors,
  background_workflows,
  background_other,
  launched_at
from agent_sessions
where tmux_socket_path = ? and tmux_server_pid = ? and tmux_pane = ? and tmux_pane != '' and ended_at = '';

-- name: ListOpenAgentSessions :many
select
  id,
  task_id,
  provider,
  tmux_socket_path,
  tmux_server_pid,
  tmux_pane,
  pane_trusted,
  provider_session_id,
  started_at,
  ended_at,
  status_phase,
  status_raw_event_name,
  status_observed_at,
  background_subagents,
  background_shells,
  background_monitors,
  background_workflows,
  background_other,
  launched_at
from agent_sessions
where task_id = ? and ended_at = '';

-- name: UpdateAgentSession :exec
update agent_sessions set
  tmux_socket_path = ?,
  tmux_server_pid = ?,
  tmux_pane = ?,
  launched_at = ?,
  pane_trusted = ?,
  provider_session_id = ?,
  status_phase = ?,
  status_raw_event_name = ?,
  status_observed_at = ?,
  background_subagents = ?,
  background_shells = ?,
  background_monitors = ?,
  background_workflows = ?,
  background_other = ?
where id = ? and ended_at = '';

-- name: EndAgentSession :exec
update agent_sessions set ended_at = ?
where id = ? and ended_at = '';

-- Times are RFC 3339 text without trailing zeros, so text order is not time
-- order within one second; julianday compares them as times.
-- name: LatestAgentSession :one
select
  id,
  task_id,
  provider,
  tmux_socket_path,
  tmux_server_pid,
  tmux_pane,
  pane_trusted,
  provider_session_id,
  started_at,
  ended_at,
  status_phase,
  status_raw_event_name,
  status_observed_at,
  background_subagents,
  background_shells,
  background_monitors,
  background_workflows,
  background_other,
  launched_at
from agent_sessions
where task_id = ?
order by julianday(started_at) desc, id desc
limit 1;
