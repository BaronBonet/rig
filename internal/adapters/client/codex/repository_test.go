package codex

import (
	"testing"
	"time"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildLaunchCommand_IncludesPrompt(t *testing.T) {
	repo := NewRepository(execx.NewFakeRunner(nil), Config{Binary: "codex"})

	cmd, err := repo.BuildLaunchCommand(&core.Task{
		Prompt: "add billing retry flow",
	})
	require.NoError(t, err)
	require.Equal(t, "codex", cmd[0])
	require.Equal(t, "add billing retry flow", cmd[len(cmd)-1])
}

func TestRepositoryLaunchRequest_UsesBinaryPromptAndTaskPrompt(t *testing.T) {
	repo := NewRepository(execx.NewFakeRunner(nil), Config{Binary: "codex"})

	launch, err := repo.LaunchRequest(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryDetectRuntimeState_ReturnsNeedsInputForPrompt(t *testing.T) {
	repo := NewRepository(execx.NewFakeRunner(nil), Config{Binary: "codex"})

	state := repo.DetectRuntimeState(core.RuntimeSnapshot{
		ForegroundCommand: "codex",
		Content:           "› add billing retry flow\n",
		ObservedAt:        time.Date(2026, 4, 6, 10, 0, 2, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 6, 10, 0, 1, 0, time.UTC),
	})
	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRepositoryProposeTaskName_TrimsRunnerOutput(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: "billing retry flow\n"},
	})
	repo := NewRepository(runner, Config{Binary: "codex"})

	name, err := repo.ProposeTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", name)
	require.Equal(t, "codex", runner.Calls[0].Name)
}

func TestRepositoryProposeTaskName_ExtractsFinalTitleFromTranscriptOutput(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: `OpenAI Codex v0.118.0 (research preview)
--------
workdir: /Users/ebon/personal_software/tmux-llm-session
model: gpt-5.4
provider: openai
--------
user
Reply with only a short task title: i want you to switch the sqlite repo to use sqlc
codex
Migrate SQLite Repo to sqlc
tokens used
26,736
`},
	})
	repo := NewRepository(runner, Config{Binary: "codex"})

	name, err := repo.ProposeTaskName(t.Context(), "i want you to switch the sqlite repo to use sqlc")
	require.NoError(t, err)
	require.Equal(t, "Migrate SQLite Repo to sqlc", name)
}

func TestRepositoryProposeTaskName_StripsMarkdownTicksFromTitle(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: "Migrate SQLite Repo to `sqlc`\n"},
	})
	repo := NewRepository(runner, Config{Binary: "codex"})

	name, err := repo.ProposeTaskName(t.Context(), "switch the sqlite repo to use sqlc")
	require.NoError(t, err)
	require.Equal(t, "Migrate SQLite Repo to sqlc", name)
}

func TestExtractCodexTitle_ReturnsLastUsefulLine(t *testing.T) {
	raw := "OpenAI Codex v0.118.0\n--------\nuser: prompt\nbilling retry flow\nexit status 1\n"

	require.Equal(t, "billing retry flow", extractCodexTitle(raw))
}
