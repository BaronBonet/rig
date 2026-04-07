package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceGetTask_ReconcilesLiveFields(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusRunning,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{SessionExists: true, AgentWindowExists: true, EditorWindowExists: true}
	before := time.Now().UTC()

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	after := time.Now().UTC()
	require.NoError(t, err)
	require.True(t, task.WorktreeExists)
	require.True(t, task.BranchExists)
	require.True(t, task.SessionExists)
	require.True(t, task.AgentWindowExists)
	require.True(t, task.EditorWindowExists)
	require.Equal(t, TaskStatusRunning, task.Status)
	requireTimeInWindow(t, task.LastReconciledAt, before, after)
	requireTimeInWindow(t, task.UpdatedAt, before, after)
}

func TestServiceGetTask_MarksTaskDegradedWhenEditorWindowMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Status:           TaskStatusRunning,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{SessionExists: true, AgentWindowExists: true}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.True(t, task.SessionExists)
	require.True(t, task.AgentWindowExists)
	require.False(t, task.EditorWindowExists)
	require.Equal(t, TaskStatus("degraded"), task.Status)
}

func TestServiceGetTask_MarksTaskBrokenWhenAgentWindowMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Status:           TaskStatusRunning,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{SessionExists: true, EditorWindowExists: true}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "missing tmux agent window")
}

func TestServiceGetTask_MarksTaskBrokenWhenSessionMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Status:           TaskStatusRunning,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "missing tmux session")
}

func TestServiceGetTask_LeavesRuntimeStateEmptyForUnsupportedProvider(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "claude",
		Status:           TaskStatusRunning,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{SessionExists: true, AgentWindowExists: true, EditorWindowExists: true}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, RuntimeStateNone, task.RuntimeState)
}

func TestServiceGetTask_LeavesRuntimeStateEmptyForBrokenTask(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
		Status:           TaskStatusRunning,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: false}
	svc.sessionClient.sessionResources = SessionResources{SessionExists: true, AgentWindowExists: true, EditorWindowExists: true}
	svc.sessionClient.snapshot = RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "› still here",
	}
	svc.providerRepo.runtimeState = RuntimeStateNeedsInput

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Equal(t, RuntimeStateNone, task.RuntimeState)
	require.True(t, task.RuntimeStateUpdatedAt.IsZero())
}

func TestServiceGetTask_EnrichesRuntimeStateForDegradedTask(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
		Status:           TaskStatusRunning,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{SessionExists: true, AgentWindowExists: true}
	svc.sessionClient.snapshot = RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "› still here",
	}
	svc.providerRepo.runtimeState = RuntimeStateNeedsInput
	before := time.Now().UTC()

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	after := time.Now().UTC()
	require.NoError(t, err)
	require.Equal(t, TaskStatusDegraded, task.Status)
	require.Equal(t, RuntimeStateNeedsInput, task.RuntimeState)
	requireTimeInWindow(t, task.RuntimeStateUpdatedAt, before, after)
}
