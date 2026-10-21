package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceReconnectTaskSession_PreservesTheInitialClaudePromptBeforeItsFirstMessage(t *testing.T) {
	for _, test := range []struct {
		name              string
		transcriptExists  bool
		hasPriorSession   bool
		hasPriorEmpty     bool
		wantInitialPrompt bool
	}{
		{name: "first prompt not yet submitted", wantInitialPrompt: true},
		{name: "repeated detach before submitting", hasPriorEmpty: true, wantInitialPrompt: true},
		{name: "existing transcript resumes", transcriptExists: true},
		{name: "later conversation is never reset", hasPriorSession: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.providerConfig.setup = multiProviderSetup()
			task := readyTaskFixture()
			task.Provider = ProviderClaude
			svc.taskRepo.listTasks = []*Task{task}
			svc.sessionClient.inspectState = TaskSessionRuntimeState{}
			path := filepath.Join(t.TempDir(), "first-session.jsonl")
			if test.transcriptExists {
				require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o600))
			}
			svc.taskRepo.latestResumeByTask[task.ID] = TaskResumeMetadata{
				TaskID: task.ID, Provider: ProviderClaude, SessionID: "first-session",
			}
			sessions := []TaskProviderSession{{
				TaskID: task.ID, Provider: ProviderClaude, ProviderSessionID: "first-session",
				TranscriptPath: path, LastEventName: HookEventSessionStart,
			}}
			if test.hasPriorSession {
				sessions = append(sessions, TaskProviderSession{Provider: ProviderClaude, ProviderSessionID: "prior"})
			}
			if test.hasPriorEmpty {
				sessions = append(sessions, TaskProviderSession{
					Provider: ProviderClaude, ProviderSessionID: "prior-empty", LastEventName: HookEventSessionStart,
					TranscriptPath: filepath.Join(t.TempDir(), "prior-empty.jsonl"),
				})
			}
			svc.taskRepo.providerSessionsByTask[task.ID] = sessions

			require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), task.ID))

			command := []string{"env", "RIG_TASK_ID=" + task.ID, "claude"}
			if test.wantInitialPrompt {
				require.Equal(t, []string{task.Prompt}, svc.sessionClient.prefilledLaunch.PrefillInput)
			} else {
				command = append(command, "resume", "first-session")
				require.Empty(t, svc.sessionClient.prefilledLaunch.PrefillInput)
			}
			require.Equal(t, command, svc.sessionClient.startedLaunch.Command)
		})
	}
}

func TestTaskServiceReconnectTaskSession_RestartsTmuxWithReconnectLaunchSpec(t *testing.T) {
	svc := newTestTaskService(t)
	svc.sessionClient.inspectState = TaskSessionRuntimeState{}
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
	require.Equal(t, []string{"env", "RIG_TASK_ID=task-1", "codex", "resume", "sess-1"},
		svc.sessionClient.startedLaunch.Command)
	require.False(t, svc.workspace.setupCalled)
	require.True(t, svc.workspace.bootstrapCalled)
}

func TestTaskServiceReconnectTaskSession_LaunchesProviderFreshWhenResumeMetadataIsMissing(t *testing.T) {
	svc := newTestTaskService(t)
	svc.sessionClient.inspectState = TaskSessionRuntimeState{}
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
	require.Equal(t, []string{"env", "RIG_TASK_ID=task-1", "codex"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput)
	require.Equal(t, 1, svc.providerRepo.sessionEnvCalls)
}

func TestTaskServiceReconnectTaskSession_LaunchesActiveProviderFreshWhenResumeMetadataIsForOldProvider(
	t *testing.T,
) {
	svc := newTestTaskService(t)
	svc.sessionClient.inspectState = TaskSessionRuntimeState{}
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
	require.Equal(t, []string{"env", "RIG_TASK_ID=task-1", "claude"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput)
}

func TestTaskServiceReconnectTaskSession_FailsClearlyWhenActiveProviderIsNotConfigured(t *testing.T) {
	svc := newTestTaskService(t)
	svc.sessionClient.inspectState = TaskSessionRuntimeState{}
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
	svc.sessionClient.inspectState = TaskSessionRuntimeState{}
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

func TestTaskServiceReconnectTaskSession_ResumesAnIdleRestoredChildWindow(t *testing.T) {
	svc := newTestTaskService(t)
	svc.sessionClient.inspectState = TaskSessionRuntimeState{}
	task := codexTaskFixture()
	task.ParentID, task.TmuxWindow = "parent", "s2"
	task.ProviderEnv = workEnv
	svc.taskRepo.listTasks = []*Task{task}
	svc.taskRepo.latestResumeByTask[task.ID] = TaskResumeMetadata{
		TaskID: task.ID, Provider: ProviderCodex, SessionID: "saved-conversation",
	}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"zsh"}}

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), task.ID))

	require.Equal(t, "s2", svc.sessionClient.startedTask.TmuxWindow)
	require.Equal(t, workEnv, svc.sessionClient.startedTask.ProviderEnv)
	require.Equal(t, []string{"env", "RIG_TASK_ID=task-1", "codex", "resume", "saved-conversation"},
		svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput)
	require.False(t, svc.workspace.setupCalled)
}

func TestTaskServiceReconnectTaskSession_DoesNotLaunchIntoARunningProvider(t *testing.T) {
	for _, command := range []string{"codex", "claude", "/opt/homebrew/bin/claude"} {
		t.Run(command, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.sessionClient.inspectState = TaskSessionRuntimeState{}
			svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
			svc.sessionClient.inspectState = TaskSessionRuntimeState{
				Exists: true, ActiveCommands: []string{"2.1.296", command},
			}

			require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))
			require.Nil(t, svc.sessionClient.startedTask)
			require.False(t, svc.workspace.bootstrapCalled)
		})
	}
}

func TestTaskServiceReconnectTaskSession_DoesNotTypeIntoBusyOrUnknownWindows(t *testing.T) {
	for name, runtime := range map[string]TaskSessionRuntimeState{
		"editor": {Exists: true, ActiveCommands: []string{"nvim"}},
		"server": {Exists: true, ActiveCommands: []string{"zsh", "node"}},
		"unknown children": {
			Exists: true, ActiveCommands: []string{"zsh"}, ChildProcessEvidenceUnavailable: true,
		},
		"empty inventory": {Exists: true},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.sessionClient.inspectState = TaskSessionRuntimeState{}
			svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
			svc.sessionClient.inspectState = runtime

			require.Error(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))
			require.Nil(t, svc.sessionClient.startedTask)
		})
	}
}

func TestTaskServiceReconnectTaskSession_UsesActiveProviderHistoryWhenResumeMetadataIsStale(t *testing.T) {
	svc := newTestTaskService(t)
	svc.sessionClient.inspectState = TaskSessionRuntimeState{}
	task := codexTaskFixture()
	svc.taskRepo.listTasks = []*Task{task}
	svc.taskRepo.latestResumeByTask[task.ID] = TaskResumeMetadata{
		TaskID: task.ID, Provider: ProviderClaude, SessionID: "other-provider",
	}
	svc.taskRepo.providerSessionsByTask[task.ID] = []TaskProviderSession{
		{Provider: ProviderCodex, ProviderSessionID: "older", LastObservedAt: time.Unix(1, 0)},
		{Provider: ProviderClaude, ProviderSessionID: "other-provider", LastObservedAt: time.Unix(3, 0)},
		{Provider: ProviderCodex, ProviderSessionID: "latest-codex", LastObservedAt: time.Unix(2, 0)},
	}

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), task.ID))
	require.Equal(t, []string{"env", "RIG_TASK_ID=task-1", "codex", "resume", "latest-codex"},
		svc.sessionClient.startedLaunch.Command)
}
