package core

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func multiProviderSetup() *ProviderSetup {
	return &ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderCodex,
	}
}

func codexTaskFixture() *Task {
	return &Task{
		ID:           "task-1",
		Prompt:       "fix billing retry flow",
		DisplayName:  "billing retry flow",
		RepoRoot:     "/tmp/repo",
		WorktreePath: "/tmp/repo-task",
		TmuxSession:  "repo_task",
		Provider:     ProviderCodex,
	}
}

func TestTaskServiceHandleHookEvent_SessionStartFromAnotherProviderLeavesTheTaskAlone(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
	svc.claudeRepo.hookUpdate = &TaskStatusUpdate{
		Phase:        TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
	}

	err := svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt: time.Now().UTC(),
		EventName:  "SessionStart",
		Provider:   ProviderClaude,
		SessionID:  "claude-sess-1",
		Cwd:        "/tmp/repo-task",
	})

	require.NoError(t, err)
	// A task is no longer adopted by the provider of a manually started agent:
	// the agent gets its own agent session instead.
	require.Nil(t, svc.taskRepo.updatedTask)
	require.Len(t, svc.taskRepo.savedProviderSessions, 1)
	require.Equal(t, ProviderClaude, svc.taskRepo.savedProviderSessions[0].Provider)
}

func TestTaskServiceHandleHookEvent_HookFromAnotherProviderRecordsHistory(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	task := codexTaskFixture()
	task.Provider = ProviderClaude
	svc.taskRepo.listTasks = []*Task{task}
	svc.providerRepo.hookUpdate = &TaskStatusUpdate{
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
	}

	err := svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt: time.Now().UTC(),
		EventName:  "PostToolUse",
		Provider:   ProviderCodex,
		SessionID:  "codex-sess-1",
		TaskID:     "task-1",
	})

	require.NoError(t, err)
	require.Len(t, svc.taskRepo.savedProviderSessions, 1)
	require.Equal(t, ProviderCodex, svc.taskRepo.savedProviderSessions[0].Provider)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceHandleHookEvent_IgnoresHooksFromUnconfiguredProviders(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}

	err := svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt: time.Now().UTC(),
		EventName:  "SessionStart",
		Provider:   ProviderClaude,
		SessionID:  "claude-sess-1",
		Cwd:        "/tmp/repo-task",
	})

	require.ErrorIs(t, err, ErrUnmanagedHookEvent)
	require.Empty(t, svc.taskRepo.savedProviderSessions)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceGetProviderSetup_ReturnsNilBeforeSetupHasRun(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = nil

	setup, err := svc.service.GetProviderSetup(t.Context())

	require.NoError(t, err)
	require.Nil(t, setup)
}

func TestTaskServiceSaveProviderSetup_InstallsHooksAndRunsProviderChecksBeforePersisting(t *testing.T) {
	svc := newTestTaskService(t)

	err := svc.service.SaveProviderSetup(t.Context(), ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderClaude,
	})

	require.NoError(t, err)
	require.Equal(t, 1, svc.providerRepo.sessionEnvCalls)
	require.Equal(t, 1, svc.claudeRepo.sessionEnvCalls)
	require.NotNil(t, svc.providerConfig.savedSetup)
	require.Equal(t, ProviderClaude, svc.providerConfig.savedSetup.Default)
}

func TestTaskServiceSaveProviderSetup_RejectsInvalidSetups(t *testing.T) {
	svc := newTestTaskService(t)

	require.ErrorContains(t,
		svc.service.SaveProviderSetup(t.Context(), ProviderSetup{}),
		"at least one configured provider",
	)
	require.ErrorContains(t,
		svc.service.SaveProviderSetup(t.Context(), ProviderSetup{
			Configured: []Provider{ProviderCodex},
			Default:    ProviderClaude,
		}),
		`default provider "claude" is not a configured provider`,
	)
	require.ErrorContains(t,
		svc.service.SaveProviderSetup(t.Context(), ProviderSetup{
			Configured: []Provider{Provider("gemini")},
			Default:    Provider("gemini"),
		}),
		`provider "gemini" is not a supported provider`,
	)
	require.Nil(t, svc.providerConfig.savedSetup)
}

func TestTaskServiceSaveProviderSetup_FailsWhenProviderChecksFail(t *testing.T) {
	svc := newTestTaskService(t)
	svc.claudeRepo.healthErr = errors.New("claude binary not found")

	err := svc.service.SaveProviderSetup(t.Context(), ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderCodex,
	})

	require.ErrorContains(t, err, "provider claude failed setup checks")
	require.Nil(t, svc.providerConfig.savedSetup)
}

func TestTaskServiceDetectProviders_ReportsPerProviderReadiness(t *testing.T) {
	svc := newTestTaskService(t)
	svc.claudeRepo.healthErr = errors.New("claude binary not found")

	detections, err := svc.service.DetectProviders(t.Context())

	require.NoError(t, err)
	require.Len(t, detections, 2)
	require.Equal(t, ProviderCodex, detections[0].Provider)
	require.True(t, detections[0].Ready)
	require.Equal(t, ProviderClaude, detections[1].Provider)
	require.False(t, detections[1].Ready)
	require.Contains(t, detections[1].Detail, "claude binary not found")
}

func TestTaskServiceHealthCheck_ValidatesConfiguredProvidersOnly(t *testing.T) {
	svc := newTestTaskService(t)
	// Claude is supported but not configured; its broken state must not fail doctor.
	svc.claudeRepo.healthErr = errors.New("claude binary not found")

	checks, err := svc.service.HealthCheck(t.Context())

	require.NoError(t, err)
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.Name)
	}
	require.Contains(t, names, "codex")
	require.NotContains(t, names, "claude")
}

func TestTaskServiceHealthCheck_FailsWhenProviderSetupIsMissing(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = nil

	checks, err := svc.service.HealthCheck(t.Context())

	require.Error(t, err)
	found := false
	for _, check := range checks {
		if check.Name == "provider setup" {
			found = true
			require.ErrorIs(t, check.Err, ErrProviderSetupRequired)
		}
	}
	require.True(t, found)
}
