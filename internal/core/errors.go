package core

import "errors"

var (
	ErrTaskNotFound        = errors.New("task not found")
	ErrTaskSessionNotFound = errors.New("task session not found")
	ErrUnmanagedHookEvent  = errors.New("unmanaged hook event")
)
