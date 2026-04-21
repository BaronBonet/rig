package core

import "errors"

var (
	ErrTaskNotFound       = errors.New("task not found")
	ErrUnmanagedHookEvent = errors.New("unmanaged hook event")
)
