//go:build legacy

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

type restoringProvider struct {
	launchRequest        LaunchRequest
	restoreLaunchRequest LaunchRequest
	seenTask             *Task
	seenHookSession      *HookSessionSummary
}

func (p *restoringProvider) IsAvailable(context.Context) error {
	return nil
}

func (p *restoringProvider) SuggestTaskName(context.Context, string) (TaskSuggestion, error) {
	return TaskSuggestion{}, nil
}

func (p *restoringProvider) LaunchRequest(task *Task) (LaunchRequest, error) {
	p.seenTask = cloneTask(task)
	return p.launchRequest, nil
}

func (p *restoringProvider) RestoreLaunchRequest(task *Task, hookSession *HookSessionSummary) (LaunchRequest, error) {
	p.seenTask = cloneTask(task)
	if hookSession != nil {
		clone := *hookSession
		p.seenHookSession = &clone
	}
	return p.restoreLaunchRequest, nil
}

func (p *restoringProvider) DetectRuntimeState(RuntimeSnapshot) RuntimeState {
	return RuntimeStateNone
}

func TestServiceOpenTask_AttachesWhenSessionExists(t *testing.T) {
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
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.openedTask.TmuxSession)
}

func TestServiceOpenTask_AllowsDegradedWhenAgentWindowExists(t *testing.T) {
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
		Status:           TaskStatus("degraded"),
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{SessionExists: true, AgentWindowExists: true}

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.openedTask.TmuxSession)
}

func TestServiceOpenTask_RestoresMissingTmuxSessionWhenWorkspaceStillExists(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	provider := &restoringProvider{
		launchRequest: LaunchRequest{
			Command:      []string{"codex"},
			Prompt:       "›",
			PrefillInput: []string{"fix billing retry flow"},
		},
		restoreLaunchRequest: LaunchRequest{
			Command: []string{"codex", "resume", "sess-1"},
			Prompt:  "›",
			SetupFiles: map[string][]byte{
				".codex/config.json": []byte(`{"restore":true}`),
			},
		},
	}
	svc.service.providers = map[string]ProviderClient{"codex": provider}
	svc.service.hooks = stubHookObservabilityRepository{
		listHookSessionSummaries: func(_ context.Context, taskIDs []string) (map[string]*HookSessionSummary, error) {
			require.Equal(t, []string{"task-1"}, taskIDs)
			return map[string]*HookSessionSummary{
				"task-1": {
					TaskID:    "task-1",
					SessionID: "sess-1",
				},
			}, nil
		},
	}
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Prompt:           "fix billing retry flow",
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
	svc.sessionClient.sessionResources = SessionResources{}

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.startedTask.TmuxSession)
	require.Equal(t, provider.restoreLaunchRequest, svc.sessionClient.startedLaunch)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.openedTask.TmuxSession)
	require.Equal(t, TaskStatusRunning, svc.taskRepo.updatedTask.Status)
	require.True(t, svc.taskRepo.updatedTask.SessionExists)
	require.True(t, svc.taskRepo.updatedTask.AgentWindowExists)
	require.True(t, svc.taskRepo.updatedTask.EditorWindowExists)
	require.NotNil(t, provider.seenHookSession)
	require.Equal(t, "sess-1", provider.seenHookSession.SessionID)

	content, readErr := os.ReadFile(filepath.Join(worktree, ".codex/config.json"))
	require.NoError(t, readErr)
	require.Equal(t, `{"restore":true}`, string(content))
}

func TestServiceOpenTask_ReturnsCleanedErrorForCleanedTask(t *testing.T) {
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: filepath.Join(t.TempDir(), "gone"),
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusCleaned,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: false, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{}

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")

	require.ErrorIs(t, err, ErrCleanedTask)
	require.Nil(t, svc.sessionClient.openedTask)
}
