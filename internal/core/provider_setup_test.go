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

func TestTaskServiceSwitchTaskProvider_LaunchesNewProviderAndUpdatesActiveProvider(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"zsh"},
	}

	task, err := svc.service.SwitchTaskProvider(t.Context(), "task-1", ProviderClaude)

	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, ProviderClaude, task.Provider)
	// Switching launches the new provider with no prompt prefill.
	require.Equal(t, []string{"claude"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput)
	// Switching bootstraps the workspace but never reruns repo seed/setup.
	require.True(t, svc.workspace.bootstrapCalled)
	require.False(t, svc.workspace.setupCalled)
	require.Equal(t, 1, svc.claudeRepo.sessionEnvCalls)
	require.NotNil(t, svc.taskRepo.updatedTask)
	require.Equal(t, ProviderClaude, svc.taskRepo.updatedTask.Provider)
}

func TestTaskServiceSwitchTaskProvider_ReconcilesStaleStatusProvider(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
	svc.taskRepo.latestByTask["task-1"] = TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
	}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"zsh"},
	}

	_, err := svc.service.SwitchTaskProvider(t.Context(), "task-1", ProviderClaude)

	require.NoError(t, err)
	// The persisted status row from the previous provider must be re-stamped:
	// a durable status/record provider mismatch puts every future TUI session
	// into a permanent reload loop until the new provider emits a hook event.
	status := svc.taskRepo.latestByTask["task-1"]
	require.Equal(t, ProviderClaude, status.Provider)
	require.Equal(t, TaskStatusPhaseWaitingForInput, status.Phase)
}

func TestTaskServiceSwitchTaskProvider_RefusesWhileCurrentProviderIsRunning(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"codex"},
	}

	task, err := svc.service.SwitchTaskProvider(t.Context(), "task-1", ProviderClaude)

	require.ErrorIs(t, err, ErrProviderSessionActive)
	require.Nil(t, task)
	require.Nil(t, svc.sessionClient.startedTask)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceSwitchTaskProvider_FailedLaunchPreservesActiveProvider(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"zsh"},
	}
	svc.sessionClient.startErr = errors.New("tmux send failed")

	task, err := svc.service.SwitchTaskProvider(t.Context(), "task-1", ProviderClaude)

	require.ErrorContains(t, err, "tmux send failed")
	require.Nil(t, task)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceSwitchTaskProvider_RejectsUnconfiguredProvider(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}

	task, err := svc.service.SwitchTaskProvider(t.Context(), "task-1", ProviderClaude)

	require.EqualError(t, err, `provider "claude" is not configured: run rig setup to enable it`)
	require.Nil(t, task)
	require.Nil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceSwitchTaskProvider_SameProviderIsANoOp(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}

	task, err := svc.service.SwitchTaskProvider(t.Context(), "task-1", ProviderCodex)

	require.NoError(t, err)
	require.Equal(t, ProviderCodex, task.Provider)
	require.Nil(t, svc.sessionClient.startedTask)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceSwitchTaskProvider_AdoptsProviderAlreadyRunningInPane(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"claude"},
	}

	task, err := svc.service.SwitchTaskProvider(t.Context(), "task-1", ProviderClaude)

	require.NoError(t, err)
	require.Equal(t, ProviderClaude, task.Provider)
	// The requested provider already owns the pane, so nothing is launched.
	require.Nil(t, svc.sessionClient.startedTask)
	require.NotNil(t, svc.taskRepo.updatedTask)
	require.Equal(t, ProviderClaude, svc.taskRepo.updatedTask.Provider)
}

func TestTaskServiceHandleHookEvent_SessionStartFromConfiguredProviderAdoptsActiveProvider(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
	svc.claudeRepo.hookUpdate = &TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     ProviderClaude,
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
	require.NotNil(t, svc.taskRepo.updatedTask)
	require.Equal(t, ProviderClaude, svc.taskRepo.updatedTask.Provider)
	// Adoption never touches Rig's tmux session reference.
	require.Equal(t, "repo_task", svc.taskRepo.updatedTask.TmuxSession)
	status := svc.taskRepo.latestByTask["task-1"]
	require.Equal(t, ProviderClaude, status.Provider)
	require.Equal(t, TaskStatusPhaseStarting, status.Phase)
}

func TestTaskServiceHandleHookEvent_LateHookFromOldProviderDoesNotDriveRuntimeStatus(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	task := codexTaskFixture()
	task.Provider = ProviderClaude
	svc.taskRepo.listTasks = []*Task{task}
	svc.providerRepo.hookUpdate = &TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     ProviderCodex,
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
	// Session history from a configured old provider is still recorded.
	require.Len(t, svc.taskRepo.savedProviderSessions, 1)
	require.Equal(t, ProviderCodex, svc.taskRepo.savedProviderSessions[0].Provider)
	// The active provider and current runtime status stay untouched.
	require.Nil(t, svc.taskRepo.updatedTask)
	_, hasStatus := svc.taskRepo.latestByTask["task-1"]
	require.False(t, hasStatus)
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
