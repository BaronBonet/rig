package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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

// The launch choice was made for the launch provider's CLI, so no other
// provider gets it.
func TestTaskLaunchChoiceFor_GivesTheChoiceToTheLaunchProviderOnly(t *testing.T) {
	task := &Task{Provider: ProviderCodex, Model: "gpt-6-luna", ReasoningLevel: "low"}

	model, reasoningLevel := task.LaunchChoiceFor(ProviderCodex)
	require.Equal(t, "gpt-6-luna", model)
	require.Equal(t, "low", reasoningLevel)

	model, reasoningLevel = task.LaunchChoiceFor(ProviderClaude)
	require.Empty(t, model)
	require.Empty(t, reasoningLevel)
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
	require.EqualError(t, err, `provider "gemini" is not configured: run rig setup to enable it`)
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

func TestTaskServiceCreateTask_LeadsTaskNameWithReferencedTicket(t *testing.T) {
	tests := []struct {
		name            string
		suggestion      TaskSuggestion
		wantDisplayName string
		wantBranchName  string
	}{
		{
			name:            "ticket and description of the work",
			suggestion:      TaskSuggestion{Name: "Fix the broken thing", BranchType: "fix", Ticket: "GH #18"},
			wantDisplayName: "GH #18 Fix the broken thing",
			wantBranchName:  "fix/gh-18-fix-the-broken-thing",
		},
		{
			name:            "ticket whose work is unknown",
			suggestion:      TaskSuggestion{BranchType: "feat", Ticket: "ABC-123"},
			wantDisplayName: "ABC-123",
			wantBranchName:  "feat/abc-123",
		},
		{
			name:            "name already led by the ticket",
			suggestion:      TaskSuggestion{Name: "abc-123 Add login retries", BranchType: "feat", Ticket: "ABC-123"},
			wantDisplayName: "abc-123 Add login retries",
			wantBranchName:  "feat/abc-123-add-login-retries",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.providerRepo.suggestedSuggestion = tt.suggestion

			task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
				Cwd:    "/tmp/repo",
				Prompt: "implement the ticket",
			}, nil)

			require.NoError(t, err)
			require.Equal(t, "/tmp/repo", svc.providerRepo.suggestRepoRoot)
			require.Equal(t, tt.wantDisplayName, task.DisplayName)
			require.Equal(t, tt.wantBranchName, task.BranchName)
		})
	}
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
	reporter.EXPECT().
		ReportTaskCreateProgress(mock.Anything, mock.Anything).
		Run(func(step TaskCreateProgressStep, _ *Task) {
			steps = append(steps, step)
		}).
		Return()

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

// failedAtStartingSessionTask is a task whose creation failed while starting
// its Session.
func failedAtStartingSessionTask() *Task {
	return &Task{
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
		CreationStep:   TaskCreateProgressStartingSession,
		CreationError:  "start task session: timed out waiting for › prompt",
	}
}

// The failed attempt, or Enter's reconnect, left the task's Session running
// by the time the user retries: its agent already runs there.
func TestTaskServiceRetryTaskCreationWithProgress_FinishesWhenTheSessionAlreadyExists(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{failedAtStartingSessionTask()}
	svc.sessionClient.startErr = fmt.Errorf("%w: repo_billing-retry-flow", ErrTaskSessionExists)

	task, err := svc.service.RetryTaskCreationWithProgress(t.Context(), "task-1", nil)

	require.NoError(t, err)
	require.Equal(t, "task-1", task.ID)
	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, TaskCreationStatusReady, tasks[0].CreationStatus)
	require.Empty(t, tasks[0].CreationError)
}

// A new task's Session must not exist yet: one that does belongs to
// something else, so creation fails rather than adopt it.
func TestTaskServiceCreateTask_FailsWhenItsSessionAlreadyExists(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.sessionClient.startErr = fmt.Errorf("%w: repo_billing-retry-flow", ErrTaskSessionExists)

	_, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.ErrorIs(t, err, ErrTaskSessionExists)
	require.Equal(t, TaskCreationStatusFailed, svc.taskRepo.updatedTask.CreationStatus)
	require.Equal(t, TaskCreateProgressStartingSession, svc.taskRepo.updatedTask.CreationStep)
}

func TestTaskServiceRetryTaskCreationWithProgress_StillFailsOnOtherSessionStartErrors(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{failedAtStartingSessionTask()}
	svc.sessionClient.startErr = errors.New("tmux send failed")

	_, err := svc.service.RetryTaskCreationWithProgress(t.Context(), "task-1", nil)

	require.ErrorContains(t, err, "tmux send failed")
	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, TaskCreationStatusFailed, tasks[0].CreationStatus)
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
		UserConfig:           svc.userConfigMock,
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
	reporter.EXPECT().
		ReportTaskCreateProgress(mock.Anything, mock.Anything).
		Run(func(step TaskCreateProgressStep, _ *Task) {
			steps = append(steps, step)
		}).
		Return()

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
	reporter.EXPECT().
		ReportTaskCreateProgress(mock.Anything, mock.Anything).
		Run(func(step TaskCreateProgressStep, _ *Task) {
			steps = append(steps, step)
		}).
		Return()

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

// A task records the model and reasoning level chosen for its launch
// provider, trimmed, and that provider's client builds its launch from them.
func TestTaskServiceCreateTask_StoresTheLaunchChoiceAndLaunchesWithIt(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.setup = &ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderCodex,
	}
	svc.claudeRepo.suggestedName = "billing retry flow"

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:            "/tmp/repo",
		Prompt:         "add billing retry flow",
		Provider:       ProviderClaude,
		Model:          "  opus[1m] ",
		ReasoningLevel: "\thigh\n",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, ProviderClaude, task.Provider)
	require.Equal(t, "opus[1m]", task.Model)
	require.Equal(t, "high", task.ReasoningLevel)
	require.Equal(t, "opus[1m]", svc.taskRepo.createdTask.Model)
	require.Equal(t, "high", svc.taskRepo.createdTask.ReasoningLevel)
	require.Equal(t, "opus[1m]", svc.taskRepo.updatedTask.Model)
	require.Equal(t, "high", svc.taskRepo.updatedTask.ReasoningLevel)
	require.NotNil(t, svc.claudeRepo.launchTask)
	require.Equal(t, "opus[1m]", svc.claudeRepo.launchTask.Model)
	require.Equal(t, "high", svc.claudeRepo.launchTask.ReasoningLevel)
	require.Equal(t, "add billing retry flow", svc.claudeRepo.launchTask.Prompt)
	require.Nil(t, svc.providerRepo.launchTask)
}

func TestTaskServiceCreateTask_FromPullRequestStoresTheLaunchChoice(t *testing.T) {
	svc := newTestTaskService(t)

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Provider: ProviderCodex,
		Source: CreateTaskSource{
			PullRequest: &RepoPullRequest{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth"},
		},
		ReasoningLevel: "xhigh",
	}, nil)

	require.NoError(t, err)
	require.Empty(t, task.Model)
	require.Equal(t, "xhigh", task.ReasoningLevel)
	require.Equal(t, "xhigh", svc.taskRepo.createdTask.ReasoningLevel)
	require.NotNil(t, svc.providerRepo.launchTask)
	require.Empty(t, svc.providerRepo.launchTask.Model)
	require.Equal(t, "xhigh", svc.providerRepo.launchTask.ReasoningLevel)
}

// A blank choice is no choice, so it needs no provider.
func TestTaskServiceCreateTask_TreatsABlankLaunchChoiceAsNone(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:            "/tmp/repo",
		Prompt:         "add billing retry flow",
		Model:          "  ",
		ReasoningLevel: "\t",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, ProviderCodex, task.Provider)
	require.Empty(t, task.Model)
	require.Empty(t, task.ReasoningLevel)
	require.Empty(t, svc.providerRepo.launchTask.Model)
	require.Empty(t, svc.providerRepo.launchTask.ReasoningLevel)
}

// A choice is made for one provider's CLI, so it must name that provider
// rather than fall to the default one, and must reach the CLI as the one
// argument it names, not as a flag.
func TestTaskServiceCreateTask_RejectsAnInvalidLaunchChoiceBeforePersisting(t *testing.T) {
	for name, test := range map[string]struct {
		input   CreateTaskInput
		wantErr string
	}{
		"model without a provider": {
			input:   CreateTaskInput{Model: "gpt-6-luna"},
			wantErr: "a model or reasoning level needs an explicit provider",
		},
		"reasoning level without a provider": {
			input:   CreateTaskInput{ReasoningLevel: "high"},
			wantErr: "a model or reasoning level needs an explicit provider",
		},
		"whitespace inside the model": {
			input:   CreateTaskInput{Provider: ProviderCodex, Model: " gpt 6 luna "},
			wantErr: `model "gpt 6 luna" must not contain whitespace or control characters`,
		},
		"newline inside the model": {
			input:   CreateTaskInput{Provider: ProviderCodex, Model: "gpt-6\nluna"},
			wantErr: `model "gpt-6\nluna" must not contain whitespace or control characters`,
		},
		"control character inside the reasoning level": {
			input:   CreateTaskInput{Provider: ProviderCodex, ReasoningLevel: "hi\x1bgh"},
			wantErr: `reasoning level "hi\x1bgh" must not contain whitespace or control characters`,
		},
		"model that reads as a flag": {
			input:   CreateTaskInput{Provider: ProviderClaude, Model: " --bogus"},
			wantErr: `model "--bogus" must not start with a dash, which the provider CLI reads as a flag`,
		},
		"reasoning level that reads as a flag": {
			input:   CreateTaskInput{Provider: ProviderCodex, ReasoningLevel: "-high"},
			wantErr: `reasoning level "-high" must not start with a dash, which the provider CLI reads as a flag`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.providerRepo.suggestedName = "billing retry flow"
			input := test.input
			input.Cwd = "/tmp/repo"
			input.Prompt = "add billing retry flow"

			task, err := svc.service.CreateTaskWithProgress(t.Context(), input, nil)

			require.Nil(t, task)
			require.EqualError(t, err, test.wantErr)
			require.Nil(t, svc.taskRepo.createdTask)
			require.Nil(t, svc.sessionClient.startedTask)
			require.Empty(t, svc.userConfig.savedLaunchDefaults)
		})
	}
}

// Retrying a failed creation launches the task with the choice it was
// created with, and keeps it on the task.
func TestTaskServiceRetryTaskCreationWithProgress_KeepsTheLaunchChoice(t *testing.T) {
	svc := newTestTaskService(t)
	failed := failedAtStartingSessionTask()
	failed.Model = "gpt-6-luna"
	failed.ReasoningLevel = "low"
	svc.taskRepo.listTasks = []*Task{failed}

	task, err := svc.service.RetryTaskCreationWithProgress(t.Context(), "task-1", nil)

	require.NoError(t, err)
	require.Equal(t, "gpt-6-luna", task.Model)
	require.Equal(t, "low", task.ReasoningLevel)
	require.Equal(t, "gpt-6-luna", svc.providerRepo.launchTask.Model)
	require.Equal(t, "low", svc.providerRepo.launchTask.ReasoningLevel)
	require.Equal(t, TaskCreationStatusReady, svc.taskRepo.updatedTask.CreationStatus)
	require.Equal(t, "gpt-6-luna", svc.taskRepo.updatedTask.Model)
	require.Equal(t, "low", svc.taskRepo.updatedTask.ReasoningLevel)
}

// The last choice becomes the Launch default, as each provider CLI keeps the
// last model picked. It is saved before the long creation steps, so it holds
// even when they fail.
func TestTaskServiceCreateTask_SavesTheLaunchChoiceAsTheLaunchProvidersDefault(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.setup = multiProviderSetup()
	svc.claudeRepo.suggestErr = errors.New("claude is offline")

	_, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		RememberLaunchChoice: true,
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		Provider:             ProviderClaude,
		Model:                "  opus[1m] ",
		ReasoningLevel:       "\thigh\n",
	}, nil)

	require.ErrorContains(t, err, "claude is offline")
	require.Equal(t, []savedLaunchDefault{
		{provider: ProviderClaude, choice: LaunchChoice{Model: "opus[1m]", ReasoningLevel: "high"}},
	}, svc.userConfig.savedLaunchDefaults)
}

func TestTaskServiceCreateTask_FromPullRequestSavesTheLaunchChoiceAsTheDefault(t *testing.T) {
	svc := newTestTaskService(t)

	_, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		RememberLaunchChoice: true,
		Cwd:                  "/tmp/repo",
		Provider:             ProviderCodex,
		Source: CreateTaskSource{
			PullRequest: &RepoPullRequest{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth"},
		},
		Model: "gpt-6-luna",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []savedLaunchDefault{
		{provider: ProviderCodex, choice: LaunchChoice{Model: "gpt-6-luna"}},
	}, svc.userConfig.savedLaunchDefaults)
}

// Going back to no choice is a choice too: it clears the Launch provider's
// Launch default.
func TestTaskServiceCreateTask_WithNoLaunchChoiceClearsTheLaunchProvidersDefault(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"

	_, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		RememberLaunchChoice: true,
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		Provider:             ProviderCodex,
		Model:                "  ",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []savedLaunchDefault{
		{provider: ProviderCodex, choice: LaunchChoice{}},
	}, svc.userConfig.savedLaunchDefaults)
}

// A task that falls to the Default provider made no choice for it, so it
// leaves that provider's Launch default alone, as when RIG_PROVIDER picks it.
func TestTaskServiceCreateTask_FallingToTheDefaultProviderSavesNoLaunchDefault(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		RememberLaunchChoice: true,
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		Model:                "  ",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, ProviderCodex, task.Provider)
	require.Empty(t, svc.userConfig.savedLaunchDefaults)
}

// The Launch default is saved only once the Launch provider is resolved.
func TestTaskServiceCreateTask_FailingBeforeTheProviderIsResolvedSavesNoLaunchDefault(t *testing.T) {
	for name, test := range map[string]struct {
		arrange  func(*testTaskServiceHarness)
		provider Provider
		wantErr  string
	}{
		"repository not detected": {
			arrange: func(svc *testTaskServiceHarness) {
				svc.repoClient.detectRepoErr = errors.New("not a git repository")
			},
			provider: ProviderCodex,
			wantErr:  "not a git repository",
		},
		"provider not configured": {
			arrange:  func(*testTaskServiceHarness) {},
			provider: ProviderClaude,
			wantErr:  `provider "claude" is not configured`,
		},
		"provider setup missing": {
			arrange: func(svc *testTaskServiceHarness) {
				svc.userConfig.setup = nil
			},
			provider: ProviderCodex,
			wantErr:  ErrProviderSetupRequired.Error(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newTestTaskService(t)
			test.arrange(svc)

			_, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
				RememberLaunchChoice: true,
				Cwd:                  "/tmp/repo",
				Prompt:               "add billing retry flow",
				Provider:             test.provider,
				Model:                "opus[1m]",
			}, nil)

			require.ErrorContains(t, err, test.wantErr)
			require.Empty(t, svc.userConfig.savedLaunchDefaults)
		})
	}
}

// The Launch default is a convenience: failing to save it never fails the
// task.
func TestTaskServiceCreateTask_SucceedsWhenSavingTheLaunchDefaultFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.userConfig.saveLaunchDefaultErr = errors.New("config directory is read-only")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		RememberLaunchChoice: true,
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		Provider:             ProviderCodex,
		ReasoningLevel:       "low",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, TaskCreationStatusReady, task.CreationStatus)
	require.Len(t, svc.userConfig.savedLaunchDefaults, 1)
	require.NotNil(t, svc.sessionClient.startedTask)
}

// A task created without asking to remember its choice, as the CLI creates
// one, makes no choice of model, so it leaves the Launch default alone even
// when it names its provider.
func TestTaskServiceCreateTask_NotRememberingTheLaunchChoiceSavesNoLaunchDefault(t *testing.T) {
	svc := newTestTaskService(t)
	svc.userConfig.setup = multiProviderSetup()
	svc.claudeRepo.suggestedName = "billing retry flow"

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add billing retry flow",
		Provider: ProviderClaude,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, ProviderClaude, task.Provider)
	require.Empty(t, svc.userConfig.savedLaunchDefaults)
}

// A retry replays the choice its task was created with rather than making a
// new one, so it leaves the Launch default alone.
func TestTaskServiceRetryTaskCreationWithProgress_DoesNotSaveTheLaunchDefault(t *testing.T) {
	svc := newTestTaskService(t)
	failed := failedAtStartingSessionTask()
	failed.Model = "gpt-6-luna"
	svc.taskRepo.listTasks = []*Task{failed}

	_, err := svc.service.RetryTaskCreationWithProgress(t.Context(), "task-1", nil)

	require.NoError(t, err)
	require.Empty(t, svc.userConfig.savedLaunchDefaults)
}

func TestTaskServiceCreateTask_CreatesTheTaskInTheTargetRepository(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name       string
		cwd        string
		targetRepo string
		wantDetect string
	}{
		{
			name:       "no target repository is the repository containing the working directory",
			cwd:        "/work/backend/internal",
			wantDetect: "/work/backend/internal",
		},
		{
			name:       "absolute path",
			cwd:        "/work/backend",
			targetRepo: "/work/frontend",
			wantDetect: "/work/frontend",
		},
		{
			name:       "absolute path is cleaned",
			cwd:        "/work/backend",
			targetRepo: "/work//frontend/",
			wantDetect: "/work/frontend",
		},
		{
			name:       "relative path is relative to the working directory",
			cwd:        "/work/backend",
			targetRepo: "../frontend",
			wantDetect: "/work/frontend",
		},
		{
			name:       "dot is the working directory",
			cwd:        "/work/backend",
			targetRepo: ".",
			wantDetect: "/work/backend",
		},
		{
			name:       "relative path below the working directory",
			cwd:        "/work",
			targetRepo: "frontend/app",
			wantDetect: "/work/frontend/app",
		},
		{
			name:       "home directory path",
			cwd:        "/work/backend",
			targetRepo: "~/code/frontend",
			wantDetect: filepath.Join(home, "code", "frontend"),
		},
		{
			name:       "home directory",
			cwd:        "/work/backend",
			targetRepo: "~",
			wantDetect: home,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.providerRepo.suggestedName = "call the new endpoint"
			svc.repoClient.repoContext = RepoContext{Root: "/work/frontend", Name: "frontend", BaseBranch: "main"}

			task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
				Cwd:        tt.cwd,
				Prompt:     "call the new endpoint",
				TargetRepo: tt.targetRepo,
			}, nil)

			require.NoError(t, err)
			require.Equal(t, []string{tt.wantDetect}, svc.repoClient.detectedCwds)
			require.Equal(t, "/work/frontend", task.RepoRoot)
			require.Equal(t, "frontend", task.RepoName)
			require.Equal(t, "/work/frontend_call-the-new-endpoint", task.WorktreePath)
			require.Equal(t, "/work/frontend", svc.providerRepo.suggestRepoRoot)
			require.Equal(t, "/work/frontend", svc.workspace.repoRoot)
		})
	}
}

// Repository detection maps a worktree to its main repository, so a task
// created in another task's worktree branches off the main checkout.
func TestTaskServiceCreateTask_CreatesTheTaskInTheMainRepositoryOfATargetWorktree(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "call the new endpoint"
	svc.repoClient.repoContext = RepoContext{Root: "/work/frontend", Name: "frontend", BaseBranch: "main"}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:        "/work/backend",
		Prompt:     "call the new endpoint",
		TargetRepo: "../frontend_add-login",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{"/work/frontend_add-login"}, svc.repoClient.detectedCwds)
	require.Equal(t, "/work/frontend", task.RepoRoot)
	require.Equal(t, "/work/frontend_call-the-new-endpoint", task.WorktreePath)
}

func TestTaskServiceCreateTask_RejectsATargetRepositoryInAnotherUsersHomeDirectory(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "call the new endpoint"

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:        "/work/backend",
		Prompt:     "call the new endpoint",
		TargetRepo: "~alice/frontend",
	}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, `repository path "~alice/frontend": only ~ and ~/ expand to the home directory`)
	require.Empty(t, svc.repoClient.detectedCwds)
	require.Nil(t, svc.taskRepo.createdTask)
}

func TestTaskServiceCreateTask_CreatesTheTaskInTheKnownRepositoryTheTargetNames(t *testing.T) {
	tests := []struct {
		name       string
		targetRepo string
		wantRoot   string
	}{
		{name: "repository in a repo directory", targetRepo: "frontend", wantRoot: "/code/frontend"},
		{name: "name in another case", targetRepo: "FrontEnd", wantRoot: "/code/frontend"},
		{name: "repository of a task outside every repo directory", targetRepo: "backend", wantRoot: "/work/backend"},
		{name: "repository named in another case by its tasks", targetRepo: "API", wantRoot: "/work/api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.providerRepo.suggestedName = "call the new endpoint"
			svc.taskRepo.listTasks = []*Task{
				backendTask(),
				{ID: "task-api", RepoRoot: "/work/api", RepoName: "Api", WorktreePath: "/work/api_fix"},
			}
			svc.userConfig.repoDirs = []string{"/code"}
			svc.repoClient.checkouts = map[string][]string{"/code": {"/code/frontend", "/code/notes"}}
			svc.repoClient.repoContext = RepoContext{
				Root:       tt.wantRoot,
				Name:       filepath.Base(tt.wantRoot),
				BaseBranch: "main",
			}

			task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
				Cwd:        "/work/backend_add-retries",
				Prompt:     "call the new endpoint",
				TargetRepo: tt.targetRepo,
			}, nil)

			require.NoError(t, err)
			require.Equal(t, []string{tt.wantRoot}, svc.repoClient.detectedCwds)
			require.Equal(t, tt.wantRoot, task.RepoRoot)
			require.Equal(t, tt.wantRoot+"_call-the-new-endpoint", task.WorktreePath)
		})
	}
}

func TestTaskServiceCreateTask_RejectsARepositoryNameSeveralKnownRepositoriesHave(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "call the new endpoint"
	svc.taskRepo.listTasks = []*Task{{ID: "task-old", RepoRoot: "/work/frontend", RepoName: "frontend"}}
	svc.userConfig.repoDirs = []string{"/code"}
	svc.repoClient.checkouts = map[string][]string{"/code": {"/code/Frontend"}}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:        "/work/backend",
		Prompt:     "call the new endpoint",
		TargetRepo: "frontend",
	}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, `2 known repositories are named "frontend", so pass the path of the one you mean `+
		`with --repo <path>: /code/Frontend, /work/frontend`)
	require.Empty(t, svc.repoClient.detectedCwds)
	require.Nil(t, svc.taskRepo.createdTask)
}

func TestTaskServiceCreateTask_RejectsARepositoryNameNoKnownRepositoryHas(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "call the new endpoint"
	svc.taskRepo.listTasks = []*Task{backendTask()}
	svc.userConfig.repoDirs = []string{"/code"}
	svc.repoClient.checkouts = map[string][]string{"/code": {"/code/notes"}}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:        "/work/backend",
		Prompt:     "call the new endpoint",
		TargetRepo: "frontend",
	}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, "no known repository is named \"frontend\": `rig repo list` shows the known "+
		"repositories, or pass the repository's path instead")
	require.Empty(t, svc.repoClient.detectedCwds)
	require.Nil(t, svc.taskRepo.createdTask)
}

// A known repository may have been moved or deleted since Rig found it.
func TestTaskServiceCreateTask_AsksForThePathOfAKnownRepositoryThatCannotBeUsed(t *testing.T) {
	tests := []struct {
		name      string
		tasks     []*Task
		checkouts map[string][]string
		root      string
		detectErr string
	}{
		{
			name:      "repository of a task that was moved",
			tasks:     []*Task{{ID: "task-old", RepoRoot: "/work/frontend", RepoName: "frontend"}},
			root:      "/work/frontend",
			detectErr: "chdir /work/frontend: no such file or directory",
		},
		{
			name:      "checkout in a repo directory that is no longer a repository",
			checkouts: map[string][]string{"/code": {"/code/frontend"}},
			root:      "/code/frontend",
			detectErr: "fatal: not a git repository (or any of the parent directories): .git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.taskRepo.listTasks = tt.tasks
			svc.userConfig.repoDirs = []string{"/code"}
			svc.repoClient.checkouts = tt.checkouts
			svc.repoClient.detectRepoErr = errors.New(tt.detectErr)

			task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
				Cwd:        "/work/backend",
				Prompt:     "call the new endpoint",
				TargetRepo: "frontend",
			}, nil)

			require.Nil(t, task)
			require.EqualError(t, err, `repository "frontend" at `+tt.root+` cannot be used (`+tt.detectErr+
				`): if it has moved, pass the repository's path instead`)
			require.Equal(t, []string{tt.root}, svc.repoClient.detectedCwds)
			require.Nil(t, svc.taskRepo.createdTask)
		})
	}
}

func TestTaskServiceCreateTask_NamesATargetRepositoryPathThatIsNoRepository(t *testing.T) {
	svc := newTestTaskService(t)
	svc.repoClient.detectRepoErr = errors.New("not a git repository")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:        "/work/backend",
		Prompt:     "call the new endpoint",
		TargetRepo: "../notes",
	}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, `repository path "../notes": not a git repository`)
	require.Equal(t, []string{"/work/notes"}, svc.repoClient.detectedCwds)
	require.Nil(t, svc.taskRepo.createdTask)
}

func TestTaskServiceCreateTask_SubmitsThePromptOnlyWhenAsked(t *testing.T) {
	for _, submit := range []bool{true, false} {
		t.Run(fmt.Sprintf("submit %t", submit), func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.providerRepo.suggestedName = "billing retry flow"

			_, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
				Cwd:          "/tmp/repo",
				Prompt:       "add billing retry flow",
				SubmitPrompt: submit,
			}, nil)

			require.NoError(t, err)
			require.Equal(t, TaskSessionLaunchSpec{
				Command:       []string{"codex"},
				ReadyMarker:   "›",
				PrefillInput:  []string{"add billing retry flow"},
				SubmitPrefill: submit,
			}, svc.sessionClient.startedLaunch)
		})
	}
}

// The choice to submit belongs to the creation that asked for it, so retrying
// that creation pastes the prompt in again without submitting it.
func TestTaskServiceRetryTaskCreation_NeverSubmitsThePrompt(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.sessionClient.startErr = errors.New("tmux failed")
	failed, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:          "/tmp/repo",
		Prompt:       "add billing retry flow",
		SubmitPrompt: true,
	}, nil)
	require.Error(t, err)
	require.True(t, svc.sessionClient.startedLaunch.SubmitPrefill)
	svc.taskRepo.listTasks = []*Task{svc.taskRepo.updatedTask}
	svc.sessionClient.startErr = nil
	svc.sessionClient.startedLaunch = TaskSessionLaunchSpec{}

	_, err = svc.service.RetryTaskCreationWithProgress(t.Context(), failed.ID, nil)

	require.NoError(t, err)
	require.Equal(t, []string{"add billing retry flow"}, svc.sessionClient.startedLaunch.PrefillInput)
	require.False(t, svc.sessionClient.startedLaunch.SubmitPrefill)
}

func TestTaskServiceReconnectTaskSession_NeverSubmitsThePrompt(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	created, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:          "/tmp/repo",
		Prompt:       "add billing retry flow",
		SubmitPrompt: true,
	}, nil)
	require.NoError(t, err)
	require.True(t, svc.sessionClient.startedLaunch.SubmitPrefill)
	svc.taskRepo.listTasks = []*Task{svc.taskRepo.updatedTask}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: false}
	svc.sessionClient.startedLaunch = TaskSessionLaunchSpec{}

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), created.ID))

	require.Equal(t, []string{"codex"}, svc.sessionClient.startedLaunch.Command)
	require.False(t, svc.sessionClient.startedLaunch.SubmitPrefill)
}

// backendTask is a task whose worktree a command creating another task may
// run in.
func backendTask() *Task {
	return &Task{
		ID:             "task-backend",
		Slug:           "add-retries",
		DisplayName:    "Add retries",
		RepoRoot:       "/work/backend",
		RepoName:       "backend",
		BranchName:     "feat/add-retries",
		WorktreePath:   "/work/backend_add-retries",
		TmuxSession:    "backend_add-retries",
		Provider:       ProviderCodex,
		CreationStatus: TaskCreationStatusReady,
	}
}

func TestTaskServiceCreateTask_LinksTheTaskItIsCreatedFrom(t *testing.T) {
	backendRepo := RepoContext{Root: "/work/backend", Name: "backend", BaseBranch: "main"}
	frontendRepo := RepoContext{Root: "/work/frontend", Name: "frontend", BaseBranch: "main"}

	tests := []struct {
		name       string
		input      CreateTaskInput
		repo       RepoContext
		wantParent string
	}{
		{
			name:       "from the calling task's worktree root",
			input:      CreateTaskInput{Cwd: "/work/backend_add-retries", LinkToCallingTask: true},
			repo:       backendRepo,
			wantParent: "task-backend",
		},
		{
			name:       "from a subdirectory of the calling task's worktree",
			input:      CreateTaskInput{Cwd: "/work/backend_add-retries/internal/api", LinkToCallingTask: true},
			repo:       backendRepo,
			wantParent: "task-backend",
		},
		{
			name: "in another repository",
			input: CreateTaskInput{
				Cwd:               "/work/backend_add-retries",
				TargetRepo:        "../frontend",
				LinkToCallingTask: true,
			},
			repo:       frontendRepo,
			wantParent: "task-backend",
		},
		{
			name: "from a pull request",
			input: CreateTaskInput{
				Cwd:               "/work/backend_add-retries",
				LinkToCallingTask: true,
				Source: CreateTaskSource{PullRequest: &RepoPullRequest{
					Number:     7,
					Title:      "Retry flow",
					BranchName: "feat/retry-flow",
				}},
			},
			repo:       backendRepo,
			wantParent: "task-backend",
		},
		{
			name:  "not from outside any task's worktree",
			input: CreateTaskInput{Cwd: "/work/backend", LinkToCallingTask: true},
			repo:  backendRepo,
		},
		{
			name:  "not from a directory beside the worktree that shares its name as a prefix",
			input: CreateTaskInput{Cwd: "/work/backend_add-retries-ui/src", LinkToCallingTask: true},
			repo:  backendRepo,
		},
		{
			name:  "not when linking is off",
			input: CreateTaskInput{Cwd: "/work/backend_add-retries/internal/api"},
			repo:  backendRepo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.providerRepo.suggestedName = "call the new endpoint"
			svc.repoClient.repoContext = tt.repo
			svc.taskRepo.listTasks = []*Task{backendTask()}
			input := tt.input
			input.Prompt = "call the new endpoint"

			task, err := svc.service.CreateTaskWithProgress(t.Context(), input, nil)

			require.NoError(t, err)
			require.Equal(t, tt.repo.Root, task.RepoRoot)
			require.Equal(t, tt.wantParent, task.ParentTaskID)
			require.Equal(t, tt.wantParent, svc.taskRepo.createdTask.ParentTaskID)
		})
	}
}
