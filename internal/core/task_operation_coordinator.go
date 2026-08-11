package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type taskOperation string

const (
	taskOperationDelete        taskOperation = "delete"
	taskOperationReconnect     taskOperation = "reconnect"
	taskOperationRetryCreation taskOperation = "retry creation"
	taskOperationSwitch        taskOperation = "switch provider"
)

func (o taskOperation) progressLabel() string {
	switch o {
	case taskOperationDelete:
		return "deleting the task"
	case taskOperationReconnect:
		return "reconnecting its session"
	case taskOperationRetryCreation:
		return "retrying task creation"
	case taskOperationSwitch:
		return "switching providers"
	default:
		return "running another operation"
	}
}

type taskOperationInProgressError struct {
	TaskID string
	Active taskOperation
}

func (e *taskOperationInProgressError) Error() string {
	return fmt.Sprintf(
		"task %q is already %s; wait for it to finish before trying again",
		e.TaskID,
		e.Active.progressLabel(),
	)
}

func (e *taskOperationInProgressError) Unwrap() error {
	return ErrTaskOperationInProgress
}

type activeTaskOperation struct {
	done chan struct{}
	err  error
	kind taskOperation
}

// taskOperationCoordinator owns the invariant that only one Task lifecycle
// mutation runs for a Task at a time. Matching idempotent operations may join
// the active operation; mutations for different Tasks remain independent.
type taskOperationCoordinator struct {
	mu     sync.Mutex
	active map[string]*activeTaskOperation
}

func newTaskOperationCoordinator() *taskOperationCoordinator {
	return &taskOperationCoordinator{active: make(map[string]*activeTaskOperation)}
}

func (c *taskOperationCoordinator) Run(
	ctx context.Context,
	taskID string,
	kind taskOperation,
	coalesce bool,
	run func(context.Context) error,
) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return run(ctx)
	}

	c.mu.Lock()
	if active := c.active[taskID]; active != nil {
		if coalesce && active.kind == kind {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-active.done:
				return active.err
			}
		}
		activeKind := active.kind
		c.mu.Unlock()
		return &taskOperationInProgressError{TaskID: taskID, Active: activeKind}
	}

	active := &activeTaskOperation{done: make(chan struct{}), kind: kind}
	c.active[taskID] = active
	c.mu.Unlock()

	err := run(ctx)

	c.mu.Lock()
	active.err = err
	delete(c.active, taskID)
	close(active.done)
	c.mu.Unlock()

	return err
}
