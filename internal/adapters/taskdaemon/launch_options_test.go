package taskdaemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestGetLaunchSettingsRoundTrip(t *testing.T) {
	t.Parallel()

	svc := newFakeTaskService()
	svc.launchSettings = &core.LaunchSettings{
		Options: map[core.Provider]core.ProviderLaunchOptions{
			core.ProviderClaude: {Models: []string{"opus"}, Efforts: []string{"max"}, Handoff: true},
		},
		Defaults: map[core.Provider]core.LaunchOptions{core.ProviderClaude: {Model: "opus"}},
	}
	client := startTestFrontend(t, svc)
	env := core.ProviderEnv{"CODEX_HOME": "/home/me/.codex-client", "CLAUDE_CONFIG_DIR": ""}

	settings, err := client.GetLaunchSettings(context.Background(), env)

	require.NoError(t, err)
	require.Equal(t, svc.launchSettings, settings)
	svc.mu.Lock()
	require.Equal(t, env, svc.launchSettingsEnv, "the window's provider env reaches the service")
	svc.mu.Unlock()

	svc.mu.Lock()
	svc.errByOp["get_launch_settings"] = errors.New("config unreadable")
	svc.mu.Unlock()
	_, err = client.GetLaunchSettings(context.Background(), nil)
	require.ErrorContains(t, err, "config unreadable")
}

func TestProviderAccountsAndUsageRoundTrip(t *testing.T) {
	t.Parallel()

	svc := newFakeTaskService()
	licentiam := core.ProviderAccount{
		Provider: core.ProviderClaude,
		Name:     "licentiam",
		Dir:      "/home/me/.claude-licentiam",
		Env:      core.ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-licentiam"},
		Window:   true,
	}
	svc.accounts = []core.ProviderAccount{licentiam}
	resets := time.Date(2026, 10, 13, 17, 0, 0, 0, time.UTC)
	svc.accountUsage = map[string]*core.AccountUsage{licentiam.Dir: {
		Plan:   "max",
		Limits: []core.UsageLimit{{Name: "5h", UsedPercent: 42, ResetsAt: resets}},
	}}
	client := startTestFrontend(t, svc)
	env := core.ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-licentiam", "CODEX_HOME": ""}

	accounts, err := client.ListProviderAccounts(context.Background(), "/home/me/dev/licentiam", env)
	require.NoError(t, err)
	require.Equal(t, svc.accounts, accounts)
	svc.mu.Lock()
	require.Equal(t, "/home/me/dev/licentiam", svc.accountsFolder)
	require.Equal(t, env, svc.accountsEnv, "the window's provider env reaches the service")
	svc.mu.Unlock()

	usage, err := client.GetAccountUsage(context.Background(), accounts[0])
	require.NoError(t, err)
	require.Equal(t, svc.accountUsage[licentiam.Dir], usage)

	svc.mu.Lock()
	svc.errByOp["get_account_usage"] = errors.New("login expired")
	svc.mu.Unlock()
	_, err = client.GetAccountUsage(context.Background(), accounts[0])
	require.ErrorContains(t, err, "login expired")
}

func TestNewTaskSessionStreamRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("streams progress before the terminal task", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createSteps = []core.TaskCreateProgressStep{
			core.TaskCreateProgressWritingHandoff,
			core.TaskCreateProgressStartingSession,
		}
		svc.createTask = &core.Task{ID: "task-1", DisplayName: "add retries", Model: "opus"}
		client := startTestFrontend(t, svc)

		events, err := client.NewTaskSessionStream(context.Background(), core.NewTaskSessionInput{
			TaskID:  "task-1",
			Prompt:  "continue",
			Model:   "opus",
			Effort:  "max",
			Handoff: true,
		})
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		require.Len(t, got, 3)
		require.Equal(t, core.TaskCreateProgressWritingHandoff, got[0].Progress.Step)
		require.Equal(t, core.TaskCreateProgressStartingSession, got[1].Progress.Step)
		require.NotNil(t, got[2].Task)
		require.Equal(t, "opus", got[2].Task.Model)
		svc.mu.Lock()
		require.Equal(t, &core.NewTaskSessionInput{
			TaskID:  "task-1",
			Prompt:  "continue",
			Model:   "opus",
			Effort:  "max",
			Handoff: true,
		}, svc.newSessionInput)
		svc.mu.Unlock()
	})

	t.Run("requires a task id", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		events, err := client.NewTaskSessionStream(context.Background(), core.NewTaskSessionInput{Prompt: "continue"})
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		require.Len(t, got, 1)
		require.ErrorContains(t, got[0].Err, "task ID required")
	})
}
