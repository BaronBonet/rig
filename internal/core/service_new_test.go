package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceNewTask_CreatesWorktreeSessionAndPersistsTask(t *testing.T) {
	svc := newTestService()
	svc.codexRepo.proposedName = "billing retry flow"

	task, err := svc.service.NewTask(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	})

	require.NoError(t, err)
	require.Equal(t, "feat/billing-retry-flow", task.BranchName)
	require.Equal(t, "/tmp/repo-billing-retry-flow", task.WorktreePath)
	require.Equal(t, "repo-billing-retry-flow", task.TmuxSession)
	require.Equal(t, "repo", task.RepoName)
	require.Equal(t, "agent", task.AgentWindowName)
	require.Equal(t, "editor", task.EditorWindowName)
	require.Equal(t, TaskStatusRunning, task.Status)
	require.Equal(t, "/tmp/repo-billing-retry-flow", svc.gitRepo.createWorktreeInput.WorktreePath)
	require.Equal(t, "repo-billing-retry-flow", svc.tmuxRepo.createdSession.SessionName)
	require.Equal(t, "agent", svc.tmuxRepo.createdSession.AgentWindowName)
	require.Equal(t, "editor", svc.tmuxRepo.createdSession.EditorWindowName)
	require.Equal(t, "repo-billing-retry-flow", svc.tmuxRepo.sentSession)
	require.Equal(t, "agent", svc.tmuxRepo.sentWindow)
	require.Equal(t, []string{"codex", "add billing retry flow"}, svc.tmuxRepo.sentCommand)
	require.Equal(t, "billing retry flow", svc.taskRepo.createdTask.DisplayName)
	require.Equal(t, "repo", svc.taskRepo.createdTask.RepoName)
	require.Equal(t, "agent", svc.taskRepo.createdTask.AgentWindowName)
	require.Equal(t, "editor", svc.taskRepo.createdTask.EditorWindowName)
}

func TestServiceNewTask_FallsBackWhenCodexNameProposalFails(t *testing.T) {
	svc := newTestService()
	svc.codexRepo.proposeErr = errors.New("codex unavailable")

	task, err := svc.service.NewTask(t.Context(), NewTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})

	require.NoError(t, err)
	require.Equal(t, "billing retry flow", task.DisplayName)
}

func TestServiceNewTask_PersistsBrokenTaskWhenTmuxCreationFails(t *testing.T) {
	svc := newTestService()
	svc.codexRepo.proposedName = "billing retry flow"
	svc.tmuxRepo.createSessionErr = errors.New("tmux failed")

	task, err := svc.service.NewTask(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	})

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "tmux failed")
	require.Equal(t, TaskStatusBroken, svc.taskRepo.updatedTask.Status)
}

func TestServiceCreateTaskWithProgress_EmitsEventsAndOpensSession(t *testing.T) {
	svc := newTestService()
	svc.codexRepo.launchCommand = []string{"codex", "add billing retry flow"}

	var events []TaskProgress
	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	}, CreateTaskOptions{OpenSession: true}, func(event TaskProgress) {
		events = append(events, event)
	})

	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", svc.tmuxRepo.attachedSession)
	require.Equal(t, []TaskProgressStep{
		TaskProgressNameSelected,
		TaskProgressWorktreeCreating,
		TaskProgressTmuxStarting,
		TaskProgressCodexLaunching,
		TaskProgressTaskCreated,
		TaskProgressSessionOpening,
	}, progressSteps(events))
	require.Equal(t, task.DisplayName, events[len(events)-2].Task.DisplayName)
}

func TestServiceCreateTaskWithProgress_SeedsWorkspaceBeforeTmux(t *testing.T) {
	svc := newTestService()
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
	require.True(t, svc.workspaceSeeder.seededBeforeTmux)
	require.Equal(t, []TaskProgressStep{
		TaskProgressNameSelected,
		TaskProgressWorktreeCreating,
		TaskProgressWorkspaceSeeding,
		TaskProgressWorkspaceSeeded,
		TaskProgressWorkspaceSeeded,
		TaskProgressTmuxStarting,
		TaskProgressCodexLaunching,
		TaskProgressTaskCreated,
	}, progressSteps(events))
	require.Equal(t, "Seeding workspace...", events[2].Message)
	require.Equal(t, "Copied .env", events[3].Message)
	require.Equal(t, "Copied local/", events[4].Message)
	require.Equal(t, TaskStatusRunning, task.Status)
}

func TestServiceNewTask_FailsBeforeCreatingTaskWhenWorkspaceValidationFails(t *testing.T) {
	svc := newTestService()
	svc.configRepo.repoConfig = RepoConfig{
		Exists: true,
		Seed:   SeedConfig{Copy: []string{".env"}},
	}
	svc.workspaceSeeder.validateErr = errors.New("invalid seed path \".env\": source path not found")

	task, err := svc.service.NewTask(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "seed workspace",
		ConfirmedDisplayName: "seed workspace",
	})

	require.Error(t, err)
	require.Nil(t, task)
	require.EqualError(t, err, "seed workspace: invalid seed path \".env\": source path not found")
	require.Nil(t, svc.taskRepo.createdTask)
	require.Equal(t, CreateWorktreeInput{}, svc.gitRepo.createWorktreeInput)
	require.Empty(t, svc.tmuxRepo.createdSession.SessionName)
}

func progressSteps(events []TaskProgress) []TaskProgressStep {
	steps := make([]TaskProgressStep, 0, len(events))
	for _, event := range events {
		steps = append(steps, event.Step)
	}

	return steps
}
