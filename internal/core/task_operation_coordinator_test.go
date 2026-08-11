package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskOperationCoordinator_CoalescesMatchingOperations(t *testing.T) {
	coordinator := newTaskOperationCoordinator()
	started := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- coordinator.Run(t.Context(), "task-1", taskOperationReconnect, true,
			func(context.Context) error {
				runs.Add(1)
				close(started)
				<-release
				return nil
			})
	}()
	<-started

	secondResult := make(chan error, 1)
	go func() {
		secondResult <- coordinator.Run(t.Context(), "task-1", taskOperationReconnect, true,
			func(context.Context) error {
				runs.Add(1)
				return nil
			})
	}()

	select {
	case err := <-secondResult:
		t.Fatalf("matching operation returned before the active operation completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)

	require.NoError(t, <-firstResult)
	require.NoError(t, <-secondResult)
	require.Equal(t, int32(1), runs.Load())
}

func TestTaskOperationCoordinator_RejectsConflictingOperation(t *testing.T) {
	coordinator := newTaskOperationCoordinator()
	started := make(chan struct{})
	release := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- coordinator.Run(t.Context(), "task-1", taskOperationReconnect, true,
			func(context.Context) error {
				close(started)
				<-release
				return nil
			})
	}()
	<-started

	err := coordinator.Run(t.Context(), "task-1", taskOperationDelete, false, func(context.Context) error {
		t.Fatal("conflicting operation must not run")
		return nil
	})

	require.ErrorIs(t, err, ErrTaskOperationInProgress)
	var inProgress *taskOperationInProgressError
	require.ErrorAs(t, err, &inProgress)
	require.Equal(t, "task-1", inProgress.TaskID)
	require.Equal(t, taskOperationReconnect, inProgress.Active)
	require.ErrorContains(t, err, "already reconnecting its session")
	require.ErrorContains(t, err, "wait for it to finish")

	close(release)
	require.NoError(t, <-firstResult)
}

func TestTaskOperationCoordinator_AllowsDifferentTasksToRunConcurrently(t *testing.T) {
	coordinator := newTaskOperationCoordinator()
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	release := make(chan struct{})
	results := make(chan error, 2)

	go func() {
		results <- coordinator.Run(t.Context(), "task-1", taskOperationReconnect, true,
			func(context.Context) error {
				close(firstStarted)
				<-release
				return nil
			})
	}()
	<-firstStarted
	go func() {
		results <- coordinator.Run(t.Context(), "task-2", taskOperationReconnect, true,
			func(context.Context) error {
				close(secondStarted)
				<-release
				return nil
			})
	}()

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("operation for another task was blocked")
	}
	close(release)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
}

func TestTaskOperationCoordinator_CoalescedWaitHonorsContextCancellation(t *testing.T) {
	coordinator := newTaskOperationCoordinator()
	started := make(chan struct{})
	release := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- coordinator.Run(t.Context(), "task-1", taskOperationReconnect, true,
			func(context.Context) error {
				close(started)
				<-release
				return nil
			})
	}()
	<-started

	waitCtx, cancel := context.WithCancel(t.Context())
	cancel()
	err := coordinator.Run(waitCtx, "task-1", taskOperationReconnect, true, func(context.Context) error {
		t.Fatal("coalesced operation must not run")
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)

	close(release)
	require.NoError(t, <-firstResult)
}
