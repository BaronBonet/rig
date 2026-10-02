package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskService_ListImportableSessionsMergesProvidersAndSkipsOwnedSessions(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = &ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderClaude,
	}
	older := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	svc.claudeRepo.folderSessions = []ProviderSessionSummary{
		{LastActiveAt: older, Provider: ProviderClaude, SessionID: "claude-owned", Cwd: "/src/code"},
		{LastActiveAt: older, Provider: ProviderClaude, SessionID: "claude-free", Cwd: "/src/code"},
	}
	svc.providerRepo.folderSessions = []ProviderSessionSummary{
		{LastActiveAt: newer, Provider: ProviderCodex, SessionID: "codex-free", Cwd: "/src/code"},
		{LastActiveAt: newer, Provider: ProviderCodex, SessionID: "elsewhere", Cwd: "/src/other"},
	}
	svc.taskRepo.listTasks = []*Task{{ID: "task-1"}}
	svc.taskRepo.providerSessionsByTask["task-1"] = []TaskProviderSession{
		{TaskID: "task-1", Provider: ProviderClaude, ProviderSessionID: "claude-owned"},
	}

	sessions, err := svc.service.ListImportableSessions(t.Context(), "/src/code")

	require.NoError(t, err)
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.SessionID)
	}
	require.Equal(t, []string{"codex-free", "claude-free"}, ids)
}

func TestTaskService_ImportSessionResumesItAsAFolderTask(t *testing.T) {
	svc := newTestTaskService(t)
	folder := t.TempDir()
	lastActive := time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)

	task, err := svc.service.ImportSession(t.Context(), ProviderSessionSummary{
		LastActiveAt:   lastActive,
		Provider:       ProviderCodex,
		SessionID:      "sess-42",
		Title:          "search-integration",
		Cwd:            folder,
		TranscriptPath: "/codex/sessions/rollout-sess-42.jsonl",
	})

	require.NoError(t, err)
	require.Equal(t, "search-integration", task.DisplayName)
	require.True(t, task.UsesFolderWorkspace())
	require.Equal(t, folder, task.WorktreePath)
	require.Equal(t, TaskCreationStatusReady, task.CreationStatus)
	require.Equal(t, ProviderCodex, task.Provider)

	require.Len(t, svc.taskRepo.savedProviderSessions, 1)
	recorded := svc.taskRepo.savedProviderSessions[0]
	require.Equal(t, task.ID, recorded.TaskID)
	require.Equal(t, "sess-42", recorded.ProviderSessionID)
	require.Equal(t, "/codex/sessions/rollout-sess-42.jsonl", recorded.TranscriptPath)
	require.Equal(t, &TaskResumeMetadata{
		ObservedAt: svc.taskRepo.savedResumeMetadata.ObservedAt,
		TaskID:     task.ID,
		SessionID:  "sess-42",
		Provider:   ProviderCodex,
	}, svc.taskRepo.savedResumeMetadata)

	require.Equal(t, task.ID, svc.sessionClient.startedTask.ID)
	require.Equal(t, []string{"codex", "resume", "sess-42"}, svc.sessionClient.startedLaunch.Command)
	require.False(t, svc.workspace.setupCalled, "an imported session's folder is never seeded")
}

func TestTaskService_ImportSessionRefusesASessionThatAlreadyBelongsToATask(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{ID: "task-1"}}
	svc.taskRepo.providerSessionsByTask["task-1"] = []TaskProviderSession{
		{TaskID: "task-1", Provider: ProviderCodex, ProviderSessionID: "sess-42"},
	}

	_, err := svc.service.ImportSession(t.Context(), ProviderSessionSummary{
		Provider:  ProviderCodex,
		SessionID: "sess-42",
		Cwd:       t.TempDir(),
	})

	require.ErrorContains(t, err, "already belongs to a task")
	require.Nil(t, svc.taskRepo.createdTask)
}

func TestTaskService_ImportSessionRefusesAMissingFolder(t *testing.T) {
	svc := newTestTaskService(t)

	_, err := svc.service.ImportSession(t.Context(), ProviderSessionSummary{
		Provider:  ProviderCodex,
		SessionID: "sess-42",
		Cwd:       "/nonexistent/folder",
	})

	require.ErrorContains(t, err, "not available")
	require.Nil(t, svc.taskRepo.createdTask)
}

func TestTaskService_ImportSessionKeepsTheTaskWhenItsSessionFailsToStart(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.reconnectLaunchErr = errTestReconnect

	task, err := svc.service.ImportSession(t.Context(), ProviderSessionSummary{
		Provider:  ProviderCodex,
		SessionID: "sess-42",
		Title:     "search-integration",
		Cwd:       t.TempDir(),
	})

	require.ErrorIs(t, err, errTestReconnect)
	require.ErrorContains(t, err, "enter retries")
	require.NotNil(t, task)
	require.NotNil(t, svc.taskRepo.createdTask)
}

var errTestReconnect = testError("resume command unavailable")

type testError string

func (e testError) Error() string { return string(e) }
