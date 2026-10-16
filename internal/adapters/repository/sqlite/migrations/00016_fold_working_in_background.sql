-- An agent whose turn ended while its background work runs now waits for
-- input, with that work still counted on its status, instead of having a
-- working_in_background phase of its own.

-- +goose Up

update agent_sessions
set status_phase = 'waiting_for_input'
where status_phase = 'working_in_background';

-- +goose Down

-- The folded statuses cannot be told apart from other turn ends with
-- background work, so they stay waiting for input.
select 1;
