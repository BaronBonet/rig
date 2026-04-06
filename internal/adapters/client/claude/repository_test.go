package claude

import (
	"testing"
	"time"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildLaunchCommand_IncludesPrompt(t *testing.T) {
	repo := NewRepository(execx.NewFakeRunner(nil), Config{Binary: "claude"})

	cmd, err := repo.BuildLaunchCommand(&core.Task{
		Prompt: "add billing retry flow",
	})
	require.NoError(t, err)
	require.Equal(t, "claude", cmd[0])
	require.Equal(t, "add billing retry flow", cmd[len(cmd)-1])
}

func TestRepositoryLaunchRequest_UsesBinaryPromptAndTaskPrompt(t *testing.T) {
	repo := NewRepository(execx.NewFakeRunner(nil), Config{Binary: "claude"})

	launch, err := repo.LaunchRequest(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.LaunchRequest{
		Command:      []string{"claude"},
		Prompt:       "❯",
		InitialInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryDetectRuntimeState_ReturnsNeedsInputForPrompt(t *testing.T) {
	repo := NewRepository(execx.NewFakeRunner(nil), Config{Binary: "claude"})

	state := repo.DetectRuntimeState(core.RuntimeSnapshot{
		ForegroundCommand: "claude",
		Content:           "❯ add billing retry flow\n",
		ObservedAt:        time.Date(2026, 4, 6, 10, 0, 2, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 6, 10, 0, 1, 0, time.UTC),
	})
	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRepositoryProposeTaskName_ParsesJSONOutput(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{
			Stdout: `{"type":"result","subtype":"success","cost_usd":0.002,"duration_ms":1500,"duration_api_ms":1200,"is_error":false,"num_turns":1,"result":"Billing Retry Flow","session_id":"abc123","total_cost_usd":0.002}` + "\n",
		},
	})
	repo := NewRepository(runner, Config{Binary: "claude"})

	name, err := repo.ProposeTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "Billing Retry Flow", name)
	require.Equal(t, "claude", runner.Calls[0].Name)
	require.Contains(t, runner.Calls[0].Args, "-p")
	require.Contains(t, runner.Calls[0].Args, "--output-format")
	require.Contains(t, runner.Calls[0].Args, "json")
}

func TestRepositoryProposeTaskName_StripsMarkdownTicks(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{
			Stdout: `{"type":"result","subtype":"success","result":"Migrate to ` + "`sqlc`" + `","is_error":false}` + "\n",
		},
	})
	repo := NewRepository(runner, Config{Binary: "claude"})

	name, err := repo.ProposeTaskName(t.Context(), "switch sqlite to sqlc")
	require.NoError(t, err)
	require.Equal(t, "Migrate to sqlc", name)
}

func TestRepositoryProposeTaskName_ReturnsErrorOnEmptyResult(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: `{"type":"result","subtype":"success","result":"","is_error":false}` + "\n"},
	})
	repo := NewRepository(runner, Config{Binary: "claude"})

	_, err := repo.ProposeTaskName(t.Context(), "do something")
	require.Error(t, err)
}

func TestRepositoryProposeTaskName_ReturnsErrorOnAPIError(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: `{"type":"result","subtype":"error","result":"API error","is_error":true}` + "\n"},
	})
	repo := NewRepository(runner, Config{Binary: "claude"})

	_, err := repo.ProposeTaskName(t.Context(), "do something")
	require.Error(t, err)
}

func TestRepositoryIsAvailable_CallsClaudeVersion(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: "1.0.0\n"},
	})
	repo := NewRepository(runner, Config{Binary: "claude"})

	err := repo.IsAvailable(t.Context())
	require.NoError(t, err)
	require.Equal(t, "claude", runner.Calls[0].Name)
	require.Equal(t, []string{"--version"}, runner.Calls[0].Args)
}
