package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests pin the TaskService port's stream contract: progress events,
// then exactly one terminal event (Task or Err), then a closed channel.

func TestTaskServiceCreateTaskStream_StreamsProgressThenTerminalTask(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"

	events, err := svc.service.CreateTaskStream(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})
	require.NoError(t, err)

	var got []TaskCreateEvent
	for event := range events {
		got = append(got, event)
	}

	require.GreaterOrEqual(t, len(got), 2, "expected at least one progress event and one terminal event")
	terminal := got[len(got)-1]
	require.NoError(t, terminal.Err)
	require.NotNil(t, terminal.Task)
	require.Equal(t, "billing retry flow", terminal.Task.DisplayName)

	var steps []TaskCreateProgressStep
	for _, event := range got[:len(got)-1] {
		require.NotNil(t, event.Progress, "only the last event may be terminal")
		require.Nil(t, event.Task)
		steps = append(steps, event.Progress.Step)
	}
	require.Contains(t, steps, TaskCreateProgressCreatingWorktree)
	require.Contains(t, steps, TaskCreateProgressStartingSession)
}

func TestTaskServiceCreateTaskStream_TerminalErrorCarriesFailedTask(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.repoClient.createErr = assertiveTestError("worktree creation failed")

	events, err := svc.service.CreateTaskStream(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})
	require.NoError(t, err)

	var got []TaskCreateEvent
	for event := range events {
		got = append(got, event)
	}

	require.NotEmpty(t, got)
	terminal := got[len(got)-1]
	require.ErrorContains(t, terminal.Err, "worktree creation failed")
	require.NotNil(t, terminal.Task, "terminal error must carry the failed task so the frontend can offer retry")
	require.Equal(t, TaskCreationStatusFailed, terminal.Task.CreationStatus)
}

func TestTaskServiceRetryTaskCreationStream_ResumesAndStreamsTerminalTask(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:             "task-1",
		Slug:           "billing-retry-flow",
		Prompt:         "add billing retry flow",
		DisplayName:    "billing retry flow",
		RepoRoot:       "/tmp/repo",
		RepoName:       "repo",
		BranchName:     "feat/billing-retry-flow",
		WorktreePath:   "/tmp/repo_billing-retry-flow",
		TmuxSession:    "repo_billing-retry-flow",
		Provider:       ProviderCodex,
		CreationStatus: TaskCreationStatusFailed,
		CreationStep:   TaskCreateProgressPreparingWorkspace,
		CreationError:  "setup workspace: setup script failed",
	}}

	retryEvents, err := svc.service.RetryTaskCreationStream(t.Context(), "task-1")
	require.NoError(t, err)

	var got []TaskCreateEvent
	for event := range retryEvents {
		got = append(got, event)
	}
	require.NotEmpty(t, got)
	terminal := got[len(got)-1]
	require.NoError(t, terminal.Err)
	require.NotNil(t, terminal.Task)
	require.Equal(t, TaskCreationStatusReady, terminal.Task.CreationStatus)
}

type assertiveTestError string

func (e assertiveTestError) Error() string {
	return string(e)
}
