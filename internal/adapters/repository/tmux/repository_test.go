package tmux

import (
	"context"
	"testing"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRepositoryCreateSession_UsesDetachedSessionInWorkingDir(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.CreateSession(context.Background(), core.CreateSessionInput{
		SessionName: "repo-billing-retry-flow",
		WorkingDir:  "/tmp/repo-billing-retry-flow",
	})
	require.NoError(t, err)
	require.Len(t, runner.Calls, 1)
	require.Equal(t, "tmux", runner.Calls[0].Name)
	require.Equal(t, []string{
		"new-session",
		"-d",
		"-s",
		"repo-billing-retry-flow",
		"-c",
		"/tmp/repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositorySendKeys_JoinsCommandAndSendsEnter(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.SendKeys(context.Background(), "repo-billing-retry-flow", []string{"codex", "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, []string{
		"send-keys",
		"-t",
		"repo-billing-retry-flow:0.0",
		"codex 'add billing retry flow'",
		"C-m",
	}, runner.Calls[0].Args)
}

func TestRepositoryAttachOrSwitch_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.AttachOrSwitch(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, []string{
		"switch-client",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositorySessionExists_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	exists, err := repo.SessionExists(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, []string{
		"has-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositoryKillSession_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.KillSession(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, []string{
		"kill-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}
