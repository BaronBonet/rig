package main

import (
	"context"
	"testing"

	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRuntimeTmuxRepositoryAttachOrSwitch_AttachesOutsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")

	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := &runtimeTmuxRepository{runner: runner, paneIDs: map[string]string{}}

	err := repo.AttachOrSwitch(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, []string{
		"attach-session",
		"-t",
		"repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRuntimeTmuxRepositoryAttachOrSwitch_SwitchesInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-123/default,123,0")

	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := &runtimeTmuxRepository{runner: runner, paneIDs: map[string]string{}}

	err := repo.AttachOrSwitch(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, []string{
		"switch-client",
		"-t",
		"repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}
