package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTaskServiceContract_ExposesCreateTaskWithProgress(t *testing.T) {
	var _ interface {
		CreateTaskWithProgress(context.Context, CreateTaskInput, TaskCreateProgressReporter) (*Task, error)
	} = (TaskService)(nil)
}

func TestCreateTaskInput_SupportsPromptAndPullRequestSources(t *testing.T) {
	promptCreate := CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add billing retry flow",
		Provider: ProviderCodex,
	}
	if promptCreate.Prompt == "" {
		t.Fatal("expected prompt-based creation input to carry a prompt")
	}

	prCreate := CreateTaskInput{
		Provider: ProviderCodex,
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

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "billing retry flow", task.DisplayName)
	require.Equal(t, "billing-retry-flow", task.Slug)
	require.Equal(t, "feat/billing-retry-flow", task.BranchName)
	require.Equal(t, "/tmp/repo_billing-retry-flow", svc.repoClient.createdTask.WorktreePath)
	require.Equal(t, "repo_billing-retry-flow", svc.sessionClient.startedTask.TmuxSession)
	require.Zero(t, svc.taskRepo.updateCount)
	require.Nil(t, svc.taskRepo.updatedTask)
	require.True(t, svc.workspace.setupCalled)
	require.True(t, svc.workspace.bootstrapCalled)
	require.True(t, svc.workspace.setupCalledBeforeSession)
	require.True(t, svc.workspace.bootstrapCalledBeforeSession)
	require.Equal(t, "/tmp/repo", svc.workspace.repoRoot)
	require.Equal(t, "/tmp/repo_billing-retry-flow", svc.workspace.worktreePath)
	require.Equal(t, svc.providerRepo.bootstrapSpec, svc.workspace.bootstrapSpec)
	require.NotNil(t, svc.providerRepo.bootstrapRequest)
	require.Equal(t, task.ID, svc.providerRepo.bootstrapRequest.ID)
	require.Equal(t, task.Slug, svc.providerRepo.bootstrapRequest.Slug)
	require.Equal(t, task.WorktreePath, svc.providerRepo.bootstrapRequest.WorktreePath)
	require.Equal(t, task.BranchName, svc.providerRepo.bootstrapRequest.BranchName)
	require.Equal(t, TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"add billing retry flow"},
	}, svc.sessionClient.startedLaunch)
}

func TestTaskServiceCreateTask_FailsWhenRequestedProviderIsUnavailable(t *testing.T) {
	svc := newTestTaskService(t)

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add billing retry flow",
		Provider: Provider("gemini"),
	}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, `provider "gemini" unavailable`)
	require.Nil(t, svc.taskRepo.createdTask)
	require.Nil(t, svc.repoClient.createdTask)
	require.False(t, svc.workspace.setupCalled)
	require.False(t, svc.workspace.bootstrapCalled)
	require.Nil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceCreateTask_FailsWhenProviderSessionEnvironmentSetupFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.providerRepo.sessionEnvErr = errors.New("codex hooks install failed")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.Error(t, err)
	require.NotNil(t, task)
	require.EqualError(t, err, "ensure task session environment: codex hooks install failed")
	require.Equal(t, 1, svc.providerRepo.sessionEnvCalls)
	require.Nil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceCreateTask_FailsWhenTaskNameSuggestionFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestErr = errors.New("codex unavailable")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, "suggest task name: codex unavailable")
	require.Nil(t, svc.taskRepo.createdTask)
	require.Nil(t, svc.repoClient.createdTask)
	require.False(t, svc.workspace.setupCalled)
	require.False(t, svc.workspace.bootstrapCalled)
	require.Nil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceCreateTask_FailsWhenTaskNameSuggestionIsEmpty(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedSuggestion = TaskSuggestion{Name: "", BranchType: "feat"}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, "suggest task name: empty task name")
	require.Nil(t, svc.taskRepo.createdTask)
	require.Nil(t, svc.repoClient.createdTask)
	require.False(t, svc.workspace.setupCalled)
	require.False(t, svc.workspace.bootstrapCalled)
	require.Nil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceCreateTask_ReturnsErrorWithoutPersistingLifecycleWhenWorkspaceBootstrapSpecFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.providerRepo.bootstrapErr = errors.New("bootstrap failed")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.Error(t, err)
	require.NotNil(t, task)
	require.Nil(t, svc.workspace.bootstrapSpec.Files)
	require.False(t, svc.workspace.setupCalled)
	require.False(t, svc.workspace.bootstrapCalled)
	require.Nil(t, svc.sessionClient.startedTask)
	require.EqualError(t, err, "build workspace bootstrap spec: bootstrap failed")
	require.Zero(t, svc.taskRepo.updateCount)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceCreateTask_ReturnsErrorWithoutPersistingLifecycleWhenWorkspaceSetupFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.workspace.setupErr = errors.New("setup script failed")
	svc.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".codex/hooks.json",
		Content:  []byte("{}"),
		FileMode: 0o644,
	}}}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.Error(t, err)
	require.NotNil(t, task)
	require.True(t, svc.workspace.setupCalled)
	require.False(t, svc.workspace.bootstrapCalled)
	require.Nil(t, svc.sessionClient.startedTask)
	require.EqualError(t, err, "setup workspace: setup script failed")
	require.Zero(t, svc.taskRepo.updateCount)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceCreateTask_BootstrapsWorkspaceWhenRepoSetupIsDisabled(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".codex/hooks.json",
		Content:  []byte("{}"),
		FileMode: 0o644,
	}}}
	svc.service = NewTaskService(TaskServiceDependencies{
		Tasks:       svc.taskRepoMock,
		GitWorktree: svc.repoClientMock,
		TmuxSession: svc.sessionClientMock,
		Providers: map[Provider]ProviderClient{
			ProviderCodex: svc.providerClientMock,
		},
		Workspace:            svc.workspaceMock,
		EnableWorkspaceSetup: false,
		DefaultProvider:      ProviderCodex,
	})

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, task)
	require.False(t, svc.workspace.setupCalled)
	require.True(t, svc.workspace.bootstrapCalled)
	require.NotNil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceCreateTask_FromPullRequestBootstrapsWorkspaceBeforeStartingSession(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".codex/hooks.json",
		Content:  []byte("{}"),
		FileMode: 0o644,
	}}}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Provider: ProviderCodex,
		Source: CreateTaskSource{
			PullRequest: &RepoPullRequest{
				Number:     42,
				Title:      "Auth rewrite",
				BranchName: "feat/auth",
				State:      PRStateDraft,
			},
		},
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, task)
	require.True(t, svc.workspace.setupCalled)
	require.True(t, svc.workspace.bootstrapCalled)
	require.True(t, svc.workspace.bootstrapCalledBeforeSession)
	require.NotNil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceCreateTask_RejectsDuplicatePullRequestBranchBeforePersist(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		RepoRoot:    "/tmp/repo",
		BranchName:  "feat/auth",
		DisplayName: "auth",
	}}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Provider: ProviderCodex,
		Source: CreateTaskSource{
			PullRequest: &RepoPullRequest{
				Number:     42,
				Title:      "Auth rewrite",
				BranchName: "feat/auth",
				State:      PRStateDraft,
			},
		},
	}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, "PR already has workspace")
	require.Nil(t, svc.taskRepo.createdTask)
	require.Nil(t, svc.repoClient.createdTask)
}

func TestTaskServiceCreateTask_ReturnsErrorWithoutPersistingLifecycleWhenRuntimeLaunchFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.sessionClient.startErr = errors.New("tmux failed")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.Error(t, err)
	require.NotNil(t, task)
	require.EqualError(t, err, "start task session: tmux failed")
	require.Zero(t, svc.taskRepo.updateCount)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceCreateTask_AppendsNumericSuffixWhenSlugAlreadyExists(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.taskRepo.listTasks = []*Task{{
		ID:           "existing-task",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		DisplayName:  "billing retry flow",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: "/tmp/repo_billing-retry-flow",
		TmuxSession:  "repo_billing-retry-flow",
	}}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "billing-retry-flow-2", task.Slug)
	require.Equal(t, "feat/billing-retry-flow-2", task.BranchName)
	require.Equal(t, "/tmp/repo_billing-retry-flow-2", task.WorktreePath)
	require.Equal(t, "repo_billing-retry-flow-2", task.TmuxSession)
}

func TestTaskServiceCreateTaskWithProgress_EmitsProgressStepsInOrder(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "task creation workflow tests"
	svc.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".codex/hooks/hooks.json",
		Content:  []byte("hooks"),
		FileMode: 0o644,
	}}}

	var steps []TaskCreateProgressStep
	reporter := NewMockTaskCreateProgressReporter(t)
	reporter.EXPECT().ReportTaskCreateProgress(mock.Anything).Run(func(step TaskCreateProgressStep) {
		steps = append(steps, step)
	}).Return()

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "testing creating a new task",
		Provider: ProviderCodex,
	}, reporter)

	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, []TaskCreateProgressStep{
		TaskCreateProgressSuggestingName,
		TaskCreateProgressCreatingWorktree,
		TaskCreateProgressPreparingWorkspace,
		TaskCreateProgressStartingSession,
	}, steps)
}

func TestTaskServiceCreateTaskWithProgress_AllowsNilReporter(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "task creation workflow tests"
	svc.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".codex/hooks/hooks.json",
		Content:  []byte("hooks"),
		FileMode: 0o644,
	}}}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "testing creating a new task",
		Provider: ProviderCodex,
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, task)
}
