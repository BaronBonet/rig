-- +goose Up

-- A Claude turn that ended with only shells, monitors or other background
-- work in flight waits on its user: that work does not bring the agent back
-- for another turn, so no Stop ever replaced these.
update task_status
set phase = 'waiting_for_input'
where provider = 'claude'
  and phase = 'working_in_background'
  and background_subagents = 0
  and background_workflows = 0;

-- An imported session resumes idle at its prompt. Tasks imported before that
-- was recognised were left starting until their user typed.
update task_status
set phase = 'waiting_for_input'
where phase = 'starting'
  and (
    select p.start_source
    from task_provider_sessions p
    where p.task_id = task_status.task_id
    order by p.last_observed_at desc
    limit 1
  ) = 'import';

-- +goose Down

-- Statuses only move forward with the next hook; there is nothing to restore.
