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

func TestTaskServiceReconnectTaskSession_RecreatesTmuxWithoutProviderWhenResumeMetadataIsMissing(t *testing.T) {
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
	require.Empty(t, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.startedLaunch.ReadyMarker)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput)
	require.Equal(t, 0, svc.providerRepo.sessionEnvCalls)
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
