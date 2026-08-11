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
	// ErrProviderSessionActive reports that a provider switch was refused
	// because the current provider process is still running in the task pane.
	ErrProviderSessionActive = errors.New("provider session is still running")
)
