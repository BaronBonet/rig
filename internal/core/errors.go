package core

import "errors"

var (
	ErrTaskNotFound = errors.New("task not found")
	ErrBrokenTask   = errors.New("task is broken")
	ErrCleanedTask  = errors.New("task is cleaned")
)
