package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// These tests drive the task creation and session launcher modules through
// their own interfaces — the cases that are awkward to reach through the
// whole service.

func failedTaskFixture(step TaskCreateProgressStep) *Task {
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
		CreationStep:   step,
		CreationError:  "boom",
	}
}

func TestTaskCreationSeam_RetryRunsOnlyRemainingSteps(t *testing.T) {
	cases := []struct {
		failedStep        TaskCreateProgressStep
		wantWorktree      bool
		wantWorkspace     bool
		wantSessionStart  bool
		wantReportedSteps []TaskCreateProgressStep
	}{
		{
			failedStep:       TaskCreateProgressCreatingWorktree,
			wantWorktree:     true,
			wantWorkspace:    true,
			wantSessionStart: true,
			wantReportedSteps: []TaskCreateProgressStep{
				TaskCreateProgressCreatingWorktree,
				TaskCreateProgressPreparingWorkspace,
				TaskCreateProgressStartingSession,
			},
		},
		{
			failedStep:       TaskCreateProgressPreparingWorkspace,
			wantWorktree:     false,
			wantWorkspace:    true,
			wantSessionStart: true,
			wantReportedSteps: []TaskCreateProgressStep{
				TaskCreateProgressPreparingWorkspace,
				TaskCreateProgressStartingSession,
			},
		},
		{
			failedStep:       TaskCreateProgressStartingSession,
			wantWorktree:     false,
			wantWorkspace:    false,
			wantSessionStart: true,
			wantReportedSteps: []TaskCreateProgressStep{
				TaskCreateProgressStartingSession,
			},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.failedStep), func(t *testing.T) {
			h := newTestTaskService(t)
			h.taskRepo.listTasks = []*Task{failedTaskFixture(tc.failedStep)}

			var reported []TaskCreateProgressStep
			reporter := NewMockTaskCreateProgressReporter(t)
			reporter.EXPECT().ReportTaskCreateProgress(mock.Anything).Run(func(step TaskCreateProgressStep) {
				reported = append(reported, step)
			}).Return()

			task, err := h.creation.RetryTaskCreationWithProgress(t.Context(), "task-1", reporter)

			require.NoError(t, err)
			require.Equal(t, TaskCreationStatusReady, task.CreationStatus)
			require.Equal(t, tc.wantReportedSteps, reported)
			require.Equal(t, tc.wantWorktree, h.repoClient.createdTask != nil, "worktree recreation")
			require.Equal(t, tc.wantWorkspace, h.workspace.setupCalled, "workspace preparation")
			require.Equal(t, tc.wantSessionStart, h.sessionClient.startedTask != nil, "session start")
		})
	}
}

func TestTaskCreationSeam_RetryRejectsNonRetryableStep(t *testing.T) {
	h := newTestTaskService(t)
	h.taskRepo.listTasks = []*Task{failedTaskFixture(TaskCreateProgressSuggestingName)}

	_, err := h.creation.RetryTaskCreationWithProgress(t.Context(), "task-1", nil)
	require.ErrorContains(t, err, "not retryable")
}

func TestTaskCreationSeam_RetryRequiresFailedCreationStatus(t *testing.T) {
	h := newTestTaskService(t)
	task := failedTaskFixture(TaskCreateProgressStartingSession)
	task.CreationStatus = TaskCreationStatusReady
	h.taskRepo.listTasks = []*Task{task}

	_, err := h.creation.RetryTaskCreationWithProgress(t.Context(), "task-1", nil)
	require.ErrorContains(t, err, "task creation is not failed")
}

func TestSessionLauncherSeam_ResolveProviderDefaultsAndGates(t *testing.T) {
	h := newTestTaskService(t)

	t.Run("empty provider resolves to the configured default", func(t *testing.T) {
		provider, client, err := h.launcher.resolveProvider(t.Context(), "")
		require.NoError(t, err)
		require.Equal(t, h.providerConfig.setup.Default, provider)
		require.NotNil(t, client)
	})

	t.Run("unconfigured provider is rejected with guidance", func(t *testing.T) {
		_, _, err := h.launcher.resolveProvider(t.Context(), Provider("mystery"))
		require.ErrorContains(t, err, `provider "mystery" is not configured`)
	})

	t.Run("missing setup requires provider setup", func(t *testing.T) {
		h.providerConfig.setup = nil
		_, _, err := h.launcher.resolveProvider(t.Context(), ProviderCodex)
		require.ErrorIs(t, err, ErrProviderSetupRequired)
	})
}

func TestSessionLauncherSeam_PrepareWorkspaceSeedsBeforeBootstrap(t *testing.T) {
	var order []string

	providerClient := NewMockProviderClient(t)
	providerClient.EXPECT().BuildWorkspaceBootstrapSpec(mock.Anything).Return(WorkspaceBootstrapSpec{}, nil)

	workspace := NewMockTaskWorkspaceManager(t)
	workspace.EXPECT().SetupTaskWorkspace(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, *Task, string) error {
			order = append(order, "setup")
			return nil
		},
	)
	workspace.EXPECT().BootstrapTaskWorkspace(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, *Task, WorkspaceBootstrapSpec) error {
			order = append(order, "bootstrap")
			return nil
		},
	)

	config := NewMockProviderConfigStore(t)
	config.EXPECT().GetProviderSetup(mock.Anything).Return(&ProviderSetup{
		Configured: []Provider{ProviderCodex},
		Default:    ProviderCodex,
	}, nil)

	launcher := newSessionLauncher(
		map[Provider]ProviderClient{ProviderCodex: providerClient},
		config,
		workspace,
		NewMockTmuxSessionClient(t),
		true,
	)

	task := failedTaskFixture(TaskCreateProgressPreparingWorkspace)
	require.NoError(t, launcher.prepareWorkspace(t.Context(), task, task.RepoRoot))
	require.Equal(t, []string{"setup", "bootstrap"}, order,
		"repo seeding must run before provider bootstrap so bootstrap files can rely on repo-local configuration")
}

func TestSessionLauncherSeam_BootstrapWorkspaceNeverSeeds(t *testing.T) {
	providerClient := NewMockProviderClient(t)
	providerClient.EXPECT().BuildWorkspaceBootstrapSpec(mock.Anything).Return(WorkspaceBootstrapSpec{}, nil)

	// No SetupTaskWorkspace expectation: mockery fails the test if seeding runs.
	workspace := NewMockTaskWorkspaceManager(t)
	workspace.EXPECT().BootstrapTaskWorkspace(mock.Anything, mock.Anything, mock.Anything).Return(nil)

	launcher := newSessionLauncher(
		map[Provider]ProviderClient{ProviderCodex: providerClient},
		NewMockProviderConfigStore(t),
		workspace,
		NewMockTmuxSessionClient(t),
		true,
	)

	task := failedTaskFixture(TaskCreateProgressPreparingWorkspace)
	require.NoError(t, launcher.bootstrapWorkspace(t.Context(), providerClient, task))
}

func TestSessionLauncherSeam_PrepareWorkspaceBootstrapsOtherConfiguredProviders(t *testing.T) {
	h := newTestTaskService(t)
	h.providerConfig.setup = &ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderCodex,
	}
	h.providerRepo.suggestedName = "billing retry flow"
	h.claudeRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".claude/settings.local.json",
		Content:  []byte(`{"hooks":{}}`),
		FileMode: 0o600,
	}}}

	task, err := h.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add billing retry flow",
		Provider: ProviderCodex,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, ProviderCodex, task.Provider)
	// Claude is not the active provider, but a codex task workspace must still
	// register claude's hooks so a manually launched claude session there is
	// observable and adoptable.
	require.True(t, h.workspace.bootstrapCalled)
	require.Len(t, h.workspace.bootstrapSpec.Files, 1)
	require.Equal(t, ".claude/settings.local.json", h.workspace.bootstrapSpec.Files[0].Path)
}

func TestServiceRefreshTaskWorkspaceHooks_BootstrapsConfiguredProvidersIntoReadyTasks(t *testing.T) {
	h := newTestTaskService(t)
	h.providerConfig.setup = &ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderCodex,
	}
	h.claudeRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{{
		Path:     ".claude/settings.local.json",
		Content:  []byte(`{"hooks":{}}`),
		FileMode: 0o600,
	}}}
	h.taskRepo.listTasks = []*Task{
		{
			ID:             "task-ready",
			Provider:       ProviderCodex,
			WorktreePath:   "/tmp/repo_a",
			CreationStatus: TaskCreationStatusReady,
		},
		{
			ID:             "task-failed",
			Provider:       ProviderCodex,
			WorktreePath:   "/tmp/repo_b",
			CreationStatus: TaskCreationStatusFailed,
		},
	}

	errs := h.service.RefreshTaskWorkspaceHooks(t.Context())

	require.Empty(t, errs)
	require.True(t, h.workspace.bootstrapCalled)
	require.Equal(t, ".claude/settings.local.json", h.workspace.bootstrapSpec.Files[0].Path)
	require.Equal(t, "/tmp/repo_a", h.workspace.worktreePath,
		"only ready tasks get their workspaces re-bootstrapped")
}
