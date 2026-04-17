package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceContract_ExposesCreateTaskWithoutProgressCallback(t *testing.T) {
	var _ interface {
		CreateTask(context.Context, CreateTaskInput) (*Task, error)
	} = (TaskService)(nil)
}

func TestCreateTaskInput_SupportsPromptAndPullRequestSources(t *testing.T) {
	promptCreate := CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add billing retry flow",
		Provider: "codex",
	}
	if promptCreate.Prompt == "" {
		t.Fatal("expected prompt-based creation input to carry a prompt")
	}

	prCreate := CreateTaskInput{
		Provider: "codex",
		Source: CreateTaskSource{
			PullRequest: &RepoPullRequest{
				Number:     42,
				Title:      "Auth rewrite",
				BranchName: "feat/auth",
				State:      PRStateDraft,
			},
		},
	}
	if prCreate.Source.PullRequest == nil {
		t.Fatal("expected create input to support pull request source metadata")
	}
}

func TestTaskServiceCreateTask_CreatesWorkspaceSessionAndPersistsTask(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"

	task, err := svc.service.CreateTask(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})

	require.NoError(t, err)
	require.Equal(t, "billing retry flow", task.DisplayName)
	require.Equal(t, "feat/billing-retry-flow", task.BranchName)
	require.Equal(t, TaskStatusRunning, task.Status)
	require.Equal(t, "/tmp/repo-billing-retry-flow", svc.repoClient.createdTask.WorktreePath)
	require.Equal(t, "repo_billing-retry-flow", svc.sessionClient.startedTask.TmuxSession)
	require.Equal(t, LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"add billing retry flow"},
	}, svc.sessionClient.startedLaunch)
}

func TestTaskServiceCreateTask_UsesConfirmedDisplayNameWhenProvided(t *testing.T) {
	svc := newTestTaskService(t)

	task, err := svc.service.CreateTask(t.Context(), CreateTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "ignored for naming",
		ConfirmedDisplayName: "billing retry flow",
		ConfirmedBranchType:  "fix",
	})

	require.NoError(t, err)
	require.Equal(t, "billing retry flow", task.DisplayName)
	require.Equal(t, "fix/billing-retry-flow", task.BranchName)
}

func TestTaskServiceCreateTask_RejectsDuplicatePullRequestBranchBeforePersist(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		RepoRoot:    "/tmp/repo",
		BranchName:  "feat/auth",
		Slug:        "auth",
		DisplayName: "auth",
	}}

	task, err := svc.service.CreateTask(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Provider: "codex",
		Source: CreateTaskSource{
			PullRequest: &RepoPullRequest{
				Number:     42,
				Title:      "Auth rewrite",
				BranchName: "feat/auth",
				State:      PRStateDraft,
			},
		},
	})

	require.Nil(t, task)
	require.EqualError(t, err, "PR already has workspace")
	require.Nil(t, svc.taskRepo.createdTask)
	require.Nil(t, svc.repoClient.createdTask)
}

func TestTaskServiceCreateTask_PersistsBrokenTaskWhenRuntimeLaunchFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.sessionClient.startErr = errors.New("tmux failed")

	task, err := svc.service.CreateTask(t.Context(), CreateTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	})

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "tmux failed")
	require.Equal(t, TaskStatusBroken, svc.taskRepo.updatedTask.Status)
}
