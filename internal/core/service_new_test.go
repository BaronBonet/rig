package core

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewService_AcceptsBusinessPorts(t *testing.T) {
	taskRepo := NewMockTaskRepository(t)
	hookRepo := &stubHookObservabilityRepository{}
	repoClient := NewMockRepoClient(t)
	sessionClient := NewMockSessionClient(t)
	providerClient := NewMockProviderClient(t)
	configRepo := NewMockRepoConfigLoader(t)
	workspaceSeeder := NewMockWorkspaceSeeder(t)

	service := NewService(
		taskRepo,
		hookRepo,
		repoClient,
		sessionClient,
		map[string]ProviderClient{"codex": providerClient},
		configRepo,
		workspaceSeeder,
		NewMockTaskWorkspaceBootstrapper(t),
		Config{Provider: "codex"},
	)

	require.NotNil(t, service)
	require.Same(t, hookRepo, service.hooks)
}

func TestServiceCreateTaskWithProgress_CreatesWorktreeSessionAndPersistsTask(t *testing.T) {
	svc := newTestService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	before := time.Now().UTC()

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	}, CreateTaskOptions{}, nil)
	after := time.Now().UTC()

	require.NoError(t, err)
	require.Equal(t, "feat/billing-retry-flow", task.BranchName)
	require.Equal(t, "/tmp/repo-billing-retry-flow", task.WorktreePath)
	require.Equal(t, "repo_billing-retry-flow", task.TmuxSession)
	require.Equal(t, "repo", task.RepoName)
	require.Equal(t, "agent", task.AgentWindowName)
	require.Equal(t, "editor", task.EditorWindowName)
	require.Equal(t, TaskStatusRunning, task.Status)
	require.Equal(t, "/tmp/repo-billing-retry-flow", svc.repoClient.createdTask.WorktreePath)
	require.Equal(t, "repo_billing-retry-flow", svc.sessionClient.startedTask.TmuxSession)
	require.Equal(t, "agent", svc.sessionClient.startedTask.AgentWindowName)
	require.Equal(t, "editor", svc.sessionClient.startedTask.EditorWindowName)
	require.Equal(t, LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"add billing retry flow"},
	}, svc.sessionClient.startedLaunch)
	require.Equal(t, "billing retry flow", svc.taskRepo.createdTask.DisplayName)
	require.Equal(t, "repo", svc.taskRepo.createdTask.RepoName)
	require.Equal(t, "agent", svc.taskRepo.createdTask.AgentWindowName)
	require.Equal(t, "editor", svc.taskRepo.createdTask.EditorWindowName)
	requireTimeInWindow(t, task.CreatedAt, before, after)
	requireTimeInWindow(t, task.UpdatedAt, before, after)
	require.False(t, task.UpdatedAt.Before(task.CreatedAt))
}

func TestServiceCreateTaskWithProgress_FallsBackWhenCodexNameProposalFails(t *testing.T) {
	svc := newTestService(t)
	svc.providerRepo.suggestErr = errors.New("codex unavailable")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, CreateTaskOptions{}, nil)

	require.NoError(t, err)
	require.Equal(t, "billing retry flow", task.DisplayName)
}

func TestCreateTask_UsesSessionClientLaunchRequest(t *testing.T) {
	svc := newTestService(t)
	svc.providerRepo.launchRequest = LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"ship it"},
	}

	_, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "ship it",
	}, CreateTaskOptions{}, nil)
	require.NoError(t, err)
	require.Equal(t, svc.providerRepo.launchRequest, svc.sessionClient.startedLaunch)
}

func TestServiceCreateTaskWithProgress_PersistsBrokenTaskWhenTmuxCreationFails(t *testing.T) {
	svc := newTestService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.sessionClient.startErr = errors.New("tmux failed")
	before := time.Now().UTC()

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	}, CreateTaskOptions{}, nil)
	after := time.Now().UTC()

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "tmux failed")
	require.Equal(t, TaskStatusBroken, svc.taskRepo.updatedTask.Status)
	requireTimeInWindow(t, task.CreatedAt, before, after)
	requireTimeInWindow(t, task.UpdatedAt, before, after)
	require.False(t, task.UpdatedAt.Before(task.CreatedAt))
}

func TestServiceCreateTaskWithProgress_EmitsEventsAndOpensSession(t *testing.T) {
	svc := newTestService(t)
	svc.providerRepo.launchRequest = LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"add billing retry flow"},
	}

	var events []TaskProgress
	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	}, CreateTaskOptions{OpenSession: true}, func(event TaskProgress) {
		events = append(events, event)
	})

	require.NoError(t, err)
	require.Equal(t, "repo_billing-retry-flow", svc.sessionClient.openedTask.TmuxSession)
	require.Equal(t, []TaskProgressStep{
		TaskProgressNameSelected,
		TaskProgressWorktreeCreating,
		TaskProgressTmuxStarting,
		TaskProgressAgentLaunching,
		TaskProgressTaskCreated,
		TaskProgressSessionOpening,
	}, progressSteps(events))
	require.Equal(t, task.DisplayName, events[len(events)-2].Task.DisplayName)
}

func TestServiceCreateTaskWithProgress_SeedsWorkspaceBeforeTmux(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Seed: SeedConfig{Copy: []string{".env", "local/"}},
	}

	var events []TaskProgress
	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "seed workspace",
		ConfirmedDisplayName: "seed workspace",
	}, CreateTaskOptions{}, func(event TaskProgress) {
		events = append(events, event)
	})

	require.NoError(t, err)
	require.Equal(t, "/tmp/repo", svc.configRepo.loadedRepoRoot)
	require.Equal(t, SeedWorkspaceInput{
		RepoRoot:      "/tmp/repo",
		WorktreePath:  "/tmp/repo-seed-workspace",
		RelativePaths: []string{".env", "local/"},
	}, svc.workspaceSeeder.seedInput)
	require.Equal(t, []string{".env", "local/"}, svc.workspaceSeeder.seededPaths)
	require.True(t, svc.workspaceSeeder.seededBeforeSession)
	require.Equal(t, []TaskProgressStep{
		TaskProgressNameSelected,
		TaskProgressWorktreeCreating,
		TaskProgressWorkspaceSeeding,
		TaskProgressWorkspaceSeeded,
		TaskProgressWorkspaceSeeded,
		TaskProgressTmuxStarting,
		TaskProgressAgentLaunching,
		TaskProgressTaskCreated,
	}, progressSteps(events))
	require.Equal(t, "Seeding workspace...", events[2].Message)
	require.Equal(t, "Copied .env", events[3].Message)
	require.Equal(t, "Copied local/", events[4].Message)
	require.Equal(t, TaskStatusRunning, task.Status)
}

func TestServiceCreateTaskWithProgress_BootstrapsManagedWorkspaceBeforeTmux(t *testing.T) {
	svc := newTestService(t)

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "test managed hooks",
		ConfirmedDisplayName: "test managed hooks",
	}, CreateTaskOptions{}, nil)

	require.NoError(t, err)
	require.NotNil(t, svc.workspaceBootstrapper.bootstrappedTask)
	require.Equal(t, task.WorktreePath, svc.workspaceBootstrapper.bootstrappedTask.WorktreePath)
	require.True(t, svc.workspaceBootstrapper.bootstrappedBeforeTmux)
}

func TestServiceCreateTaskWithProgress_FailsBeforeCreatingTaskWhenWorkspaceValidationFails(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Exists: true,
		Seed:   SeedConfig{Copy: []string{".env"}},
	}
	svc.workspaceSeeder.validateErr = errors.New("invalid seed path \".env\": source path not found")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "seed workspace",
		ConfirmedDisplayName: "seed workspace",
	}, CreateTaskOptions{}, nil)

	require.Error(t, err)
	require.Nil(t, task)
	require.EqualError(t, err, "seed workspace: invalid seed path \".env\": source path not found")
	require.Nil(t, svc.taskRepo.createdTask)
	require.Nil(t, svc.repoClient.createdTask)
	require.Nil(t, svc.sessionClient.startedTask)
}

func progressSteps(events []TaskProgress) []TaskProgressStep {
	steps := make([]TaskProgressStep, 0, len(events))
	for _, event := range events {
		steps = append(steps, event.Step)
	}

	return steps
}
