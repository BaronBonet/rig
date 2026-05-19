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
	require.Equal(t, 1, svc.taskRepo.updateCount)
	require.Equal(t, TaskCreationStatusReady, svc.taskRepo.updatedTask.CreationStatus)
	require.Empty(t, svc.taskRepo.updatedTask.CreationError)
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

func TestTaskServiceCreateTask_EnsuresProviderSessionEnvironmentBeforeStartingSession(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.NoError(t, err)
	require.NotNil(t, task)
	require.Contains(t, svc.events, "ensure_task_session_environment")
	require.Contains(t, svc.events, "start_task_session")
	require.Less(
		t,
		indexOfEvent(svc.events, "ensure_task_session_environment"),
		indexOfEvent(svc.events, "start_task_session"),
	)
}

func indexOfEvent(events []string, target string) int {
	for i, event := range events {
		if event == target {
			return i
		}
	}
	return -1
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
	require.Equal(t, 1, svc.taskRepo.updateCount)
	require.Equal(t, TaskCreationStatusFailed, svc.taskRepo.updatedTask.CreationStatus)
	require.Equal(t, TaskCreateProgressPreparingWorkspace, svc.taskRepo.updatedTask.CreationStep)
	require.Equal(t, "build workspace bootstrap spec: bootstrap failed", svc.taskRepo.updatedTask.CreationError)
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
	require.Equal(t, 1, svc.taskRepo.updateCount)
	require.Equal(t, TaskCreationStatusFailed, svc.taskRepo.updatedTask.CreationStatus)
	require.Equal(t, TaskCreateProgressPreparingWorkspace, svc.taskRepo.updatedTask.CreationStep)
	require.Equal(t, "setup workspace: setup script failed", svc.taskRepo.updatedTask.CreationError)
}

func TestTaskServiceRetryTaskCreationWithProgress_ResumesPreparingWorkspaceFailure(t *testing.T) {
	svc := newTestTaskService(t)
	failedTask := &Task{
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
	}
	svc.taskRepo.listTasks = []*Task{failedTask}
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

	task, err := svc.service.RetryTaskCreationWithProgress(t.Context(), "task-1", reporter)

	require.NoError(t, err)
	require.Equal(t, "task-1", task.ID)
	require.Equal(t, []TaskCreateProgressStep{
		TaskCreateProgressPreparingWorkspace,
		TaskCreateProgressStartingSession,
	}, steps)
	require.True(t, svc.workspace.setupCalled)
	require.True(t, svc.workspace.bootstrapCalled)
	require.NotNil(t, svc.sessionClient.startedTask)
	require.Equal(t, 3, svc.taskRepo.updateCount)
	require.Equal(t, TaskCreationStatusReady, svc.taskRepo.updatedTask.CreationStatus)
	require.Empty(t, svc.taskRepo.updatedTask.CreationError)
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
	require.Equal(t, 42, svc.repoClient.createdPRNumber)
}

func TestTaskServiceCreateTask_FromPullRequestEmitsProgressStepsInOrder(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".codex/hooks.json",
		Content:  []byte("{}"),
		FileMode: 0o644,
	}}}

	var steps []TaskCreateProgressStep
	reporter := NewMockTaskCreateProgressReporter(t)
	reporter.EXPECT().ReportTaskCreateProgress(mock.Anything).Run(func(step TaskCreateProgressStep) {
		steps = append(steps, step)
	}).Return()

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
	}, reporter)

	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, []TaskCreateProgressStep{
		TaskCreateProgressCreatingWorktree,
		TaskCreateProgressPreparingWorkspace,
		TaskCreateProgressStartingSession,
	}, steps)
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
	require.Equal(t, 1, svc.taskRepo.updateCount)
	require.Equal(t, TaskCreationStatusFailed, svc.taskRepo.updatedTask.CreationStatus)
	require.Equal(t, TaskCreateProgressStartingSession, svc.taskRepo.updatedTask.CreationStep)
	require.Equal(t, "start task session: tmux failed", svc.taskRepo.updatedTask.CreationError)
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
