package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// workEnv is the configuration of a rig window started under a second Claude
// Code account, while the daemon may run under another.
var workEnv = ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-work", "CODEX_HOME": ""}

func TestTaskServiceCreateTask_RunsTheTaskWithTheWindowsProviderConfiguration(t *testing.T) {
	for name, input := range map[string]CreateTaskInput{
		"worktree": {Cwd: "/tmp/repo", Prompt: "add billing retry flow"},
		"folder":   {Cwd: "/src/api", Prompt: "triage flaky specs", Workspace: WorkspaceKindFolder},
		"pull request": {Cwd: "/tmp/repo", Source: CreateTaskSource{PullRequest: &RepoPullRequest{
			Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: PRStateDraft,
		}}},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.providerRepo.suggestedName = "billing retry flow"
			input.ProviderEnv = workEnv

			task, err := svc.service.CreateTaskWithProgress(t.Context(), input, nil)

			require.NoError(t, err)
			require.Equal(t, workEnv, task.ProviderEnv)
			require.Equal(t, workEnv, svc.taskRepo.createdTask.ProviderEnv)
			require.Equal(t, workEnv, svc.providerRepo.sessionEnvEnv)
			require.Equal(t, workEnv, svc.sessionClient.startedTask.ProviderEnv)
			if input.Source.PullRequest == nil {
				require.Equal(
					t,
					workEnv,
					svc.providerRepo.suggestEnv,
					"the name suggestion runs as the window's account",
				)
			}
		})
	}
}

func TestTaskServiceReconnectTaskSession_KeepsTheTasksProviderConfiguration(t *testing.T) {
	svc := newTestTaskService(t)
	task := codexTaskFixture()
	task.ProviderEnv = workEnv
	svc.taskRepo.listTasks = []*Task{task}
	svc.taskRepo.latestResumeByTask["task-1"] = TaskResumeMetadata{
		TaskID: "task-1", Provider: ProviderCodex, SessionID: "sess-1",
	}

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, workEnv, svc.providerRepo.sessionEnvEnv)
	require.Equal(t, workEnv, svc.sessionClient.startedTask.ProviderEnv)
}

func TestTaskServiceSwitchTaskProvider_KeepsTheTasksProviderConfiguration(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	task := codexTaskFixture()
	task.ProviderEnv = workEnv
	svc.taskRepo.listTasks = []*Task{task}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"zsh"}}

	_, err := svc.service.SwitchTaskProvider(t.Context(), "task-1", ProviderClaude)

	require.NoError(t, err)
	require.Equal(t, workEnv, svc.claudeRepo.sessionEnvEnv)
	require.Equal(t, workEnv, svc.sessionClient.startedTask.ProviderEnv)
}

func TestProviderEnv_LookupTellsUnsetFromUndecided(t *testing.T) {
	value, decided := workEnv.Lookup("CODEX_HOME")
	require.True(t, decided)
	require.Empty(t, value)

	_, decided = workEnv.Lookup("GEMINI_HOME")
	require.False(t, decided)

	_, decided = ProviderEnv(nil).Lookup("CODEX_HOME")
	require.False(t, decided)
}
