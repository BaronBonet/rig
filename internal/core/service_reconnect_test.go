package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceReconnectTaskSession_RestartsTmuxWithReconnectLaunchSpec(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Prompt:       "fix billing retry flow",
		DisplayName:  "billing retry flow",
		RepoRoot:     "/tmp/repo",
		WorktreePath: "/tmp/repo-task",
		TmuxSession:  "repo_task",
		Provider:     ProviderCodex,
	}}
	svc.taskRepo.latestResumeByTask["task-1"] = TaskResumeMetadata{
		TaskID:    "task-1",
		Provider:  ProviderCodex,
		SessionID: "sess-1",
	}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.NoError(t, err)
	require.NotNil(t, svc.sessionClient.startedTask)
	require.Equal(t, "task-1", svc.sessionClient.startedTask.ID)
	require.Equal(t, []string{"codex", "resume", "sess-1"}, svc.sessionClient.startedLaunch.Command)
	require.True(t, svc.workspace.bootstrapCalled)
}

func TestTaskServiceReconnectTaskSession_LaunchesProviderFreshWhenResumeMetadataIsMissing(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Prompt:       "fix billing retry flow",
		DisplayName:  "billing retry flow",
		RepoRoot:     "/tmp/repo",
		WorktreePath: "/tmp/repo-task",
		TmuxSession:  "repo_task",
		Provider:     ProviderCodex,
	}}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.NoError(t, err)
	require.NotNil(t, svc.sessionClient.startedTask)
	require.Equal(t, "task-1", svc.sessionClient.startedTask.ID)
	require.Equal(t, []string{"codex"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput)
	require.Equal(t, 1, svc.providerRepo.sessionEnvCalls)
}

func TestTaskServiceReconnectTaskSession_LaunchesActiveProviderFreshWhenResumeMetadataIsForOldProvider(
	t *testing.T,
) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = &ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderCodex,
	}
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Prompt:       "fix billing retry flow",
		DisplayName:  "billing retry flow",
		RepoRoot:     "/tmp/repo",
		WorktreePath: "/tmp/repo-task",
		TmuxSession:  "repo_task",
		Provider:     ProviderClaude,
	}}
	svc.taskRepo.latestResumeByTask["task-1"] = TaskResumeMetadata{
		TaskID:    "task-1",
		Provider:  ProviderCodex,
		SessionID: "sess-codex",
	}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.NoError(t, err)
	require.Equal(t, []string{"claude"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput)
}

func TestTaskServiceReconnectTaskSession_FailsClearlyWhenActiveProviderIsNotConfigured(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		DisplayName:  "billing retry flow",
		RepoRoot:     "/tmp/repo",
		WorktreePath: "/tmp/repo-task",
		TmuxSession:  "repo_task",
		Provider:     ProviderClaude,
	}}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err, `provider "claude" is not configured: run rig setup to enable it`)
	require.Nil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceReconnectTaskSession_FailsWhenProviderSessionEnvironmentSetupFails(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Prompt:       "fix billing retry flow",
		DisplayName:  "billing retry flow",
		RepoRoot:     "/tmp/repo",
		WorktreePath: "/tmp/repo-task",
		TmuxSession:  "repo_task",
		Provider:     ProviderCodex,
	}}
	svc.taskRepo.latestResumeByTask["task-1"] = TaskResumeMetadata{
		TaskID:    "task-1",
		Provider:  ProviderCodex,
		SessionID: "sess-1",
	}
	svc.providerRepo.sessionEnvErr = errors.New("codex hooks install failed")

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err, "ensure task session environment: codex hooks install failed")
	require.Equal(t, 1, svc.providerRepo.sessionEnvCalls)
	require.Nil(t, svc.sessionClient.startedTask)
}
