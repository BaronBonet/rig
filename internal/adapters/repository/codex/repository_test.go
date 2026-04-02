package codex

import (
	"testing"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildLaunchCommand_IncludesPrompt(t *testing.T) {
	repo := NewRepository(execx.NewFakeRunner(nil), "codex")

	cmd, err := repo.BuildLaunchCommand(&core.Task{
		Prompt: "add billing retry flow",
	})
	require.NoError(t, err)
	require.Equal(t, "codex", cmd[0])
	require.Equal(t, "add billing retry flow", cmd[len(cmd)-1])
}

func TestRepositoryProposeTaskName_TrimsRunnerOutput(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: "billing retry flow\n"},
	})
	repo := NewRepository(runner, "codex")

	name, err := repo.ProposeTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", name)
	require.Equal(t, "codex", runner.Calls[0].Name)
}
