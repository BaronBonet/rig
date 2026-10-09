package core

import "errors"

var (
	ErrTaskNotFound        = errors.New("task not found")
	ErrTaskSessionNotFound = errors.New("task session not found")
	// ErrTaskOperationInProgress reports that a Task lifecycle mutation was
	// refused because another mutation is already active for the same Task.
	ErrTaskOperationInProgress = errors.New("task operation already in progress")
	ErrUnmanagedHookEvent      = errors.New("unmanaged hook event")
	// ErrProviderSetupRequired reports that provider setup has never completed,
	// so no provider is configured for task work.
	ErrProviderSetupRequired = errors.New("provider setup required: run rig setup")
	// ErrTaskSessionExists reports that a task's tmux session could not be
	// started because it already exists. Rig never types into a Session it
	// did not just create, where an agent may be running.
	ErrTaskSessionExists = errors.New("task session already exists")
)
