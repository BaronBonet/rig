package codex

import (
	"context"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/require"
)

func TestRepositoryLaunchSpecs_PassTheTasksModelAndEffortBeforeAnySubcommand(t *testing.T) {
	codexHome := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }
	task := &core.Task{Prompt: "add billing retry flow", Model: "gpt-5", Effort: "high"}

	start, err := repo.BuildTaskSessionLaunchSpec(task)
	require.NoError(t, err)
	require.Equal(t,
		[]string{"env", "CODEX_HOME=" + codexHome, "codex", "-m", "gpt-5", "-c", "model_reasoning_effort=high"},
		start.Command,
	)

	resume, err := repo.BuildReconnectTaskSessionLaunchSpec(task, "sess-1")
	require.NoError(t, err)
	require.Equal(t,
		[]string{
			"env", "CODEX_HOME=" + codexHome, "codex",
			"-m", "gpt-5", "-c", "model_reasoning_effort=high", "resume", "sess-1",
		},
		resume.Command,
	)
}

func TestRepositoryLaunchOptions_ListsModelsAndEffortsWithoutHandoffs(t *testing.T) {
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)

	options := repo.LaunchOptions()

	require.Equal(t, []string{"gpt-5-codex", "gpt-5"}, options.Models)
	require.Equal(t, []string{"low", "medium", "high"}, options.Efforts)
	require.False(t, options.Handoff)
	require.Equal(t, "/quit", repo.ExitCommand())

	_, err := repo.WriteSessionHandoff(context.Background(), &core.Task{}, core.TaskProviderSession{}, "")
	require.ErrorIs(t, err, core.ErrHandoffUnsupported)
}
