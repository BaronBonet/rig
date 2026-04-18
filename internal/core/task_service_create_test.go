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
	svc.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".codex/hooks/hooks.json",
		Content:  []byte("hooks"),
		FileMode: 0o644,
	}}}

	task, err := svc.service.CreateTask(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})

	require.NoError(t, err)
	require.Equal(t, "billing retry flow", task.DisplayName)
	require.Equal(t, "feat/"+task.ID, task.BranchName)
	require.Equal(t, TaskStatusRunning, task.Status)
	require.Equal(t, "/tmp/repo-"+task.ID, svc.repoClient.createdTask.WorktreePath)
	require.Equal(t, "repo_"+task.ID, svc.sessionClient.startedTask.TmuxSession)
	require.True(t, svc.preparer.called)
	require.True(t, svc.preparer.calledBeforeSession)
	require.Equal(t, "/tmp/repo", svc.preparer.repoRoot)
	require.Equal(t, "/tmp/repo-"+task.ID, svc.preparer.worktreePath)
	require.Equal(t, svc.providerRepo.bootstrapSpec, svc.preparer.bootstrapSpec)
	require.NotNil(t, svc.providerRepo.bootstrapRequest)
	require.Equal(t, task.ID, svc.providerRepo.bootstrapRequest.ID)
	require.Equal(t, TaskStatusCreating, svc.providerRepo.bootstrapRequest.Status)
	require.Equal(t, task.WorktreePath, svc.providerRepo.bootstrapRequest.WorktreePath)
	require.Equal(t, task.BranchName, svc.providerRepo.bootstrapRequest.BranchName)
	require.Equal(t, TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		InitialInput: []string{"add billing retry flow"},
	}, svc.sessionClient.startedLaunch)
}

func TestTaskServiceCreateTask_PersistsBrokenTaskWhenWorkspaceBootstrapSpecFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.providerRepo.bootstrapErr = errors.New("bootstrap failed")

	task, err := svc.service.CreateTask(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})

	require.Error(t, err)
	require.Nil(t, svc.preparer.bootstrapSpec.Files)
	require.False(t, svc.preparer.called)
	require.Nil(t, svc.sessionClient.startedTask)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "build workspace bootstrap spec")
	require.Contains(t, task.LastError, "bootstrap failed")
	require.Equal(t, TaskStatusBroken, svc.taskRepo.updatedTask.Status)
}

func TestTaskServiceCreateTask_PersistsBrokenTaskWhenWorkspacePreparationFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.preparer.prepareErr = errors.New("setup script failed")
	svc.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".claude/settings.local.json",
		Content:  []byte("{}"),
		FileMode: 0o644,
	}}}

	task, err := svc.service.CreateTask(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})

	require.Error(t, err)
	require.True(t, svc.preparer.called)
	require.Equal(t, svc.providerRepo.bootstrapSpec, svc.preparer.bootstrapSpec)
	require.Nil(t, svc.sessionClient.startedTask)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "prepare workspace")
	require.Contains(t, task.LastError, "setup script failed")
	require.Equal(t, TaskStatusBroken, svc.taskRepo.updatedTask.Status)
}

func TestTaskServiceCreateTask_RejectsDuplicatePullRequestBranchBeforePersist(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		RepoRoot:    "/tmp/repo",
		BranchName:  "feat/auth",
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
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "tmux failed")
	require.Equal(t, TaskStatusBroken, svc.taskRepo.updatedTask.Status)
}
