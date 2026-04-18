package claude

import (
	"os"
	"testing"
	"time"

	"rig/internal/core"
	"rig/internal/pkg/execx"
	"rig/internal/pkg/prompts"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildLaunchCommand_IncludesPrompt(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t), Config{Binary: "claude"})

	cmd, err := repo.BuildLaunchCommand(&core.Task{
		Prompt: "add billing retry flow",
	})
	require.NoError(t, err)
	require.Equal(t, "claude", cmd[0])
	require.Equal(t, "add billing retry flow", cmd[len(cmd)-1])
}

func TestRepositoryBuildTaskSessionLaunchSpec_UsesBinaryPromptAndTaskPrompt(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t), Config{Binary: "claude"})

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:      []string{"claude"},
		ReadyMarker:  "❯",
		PrefillInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryBuildTaskSessionLaunchSpec_OmitsPrefillInputWhenTaskPromptIsEmpty(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t), Config{Binary: "claude"})

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: ""})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:     []string{"claude"},
		ReadyMarker: "❯",
	}, launch)
}

func TestRepositoryBuildWorkspaceBootstrapSpec_RendersClaudeSettings(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t), Config{
		Binary:         "claude",
		HookListenAddr: "127.0.0.1:4000",
	})

	spec, err := repo.BuildWorkspaceBootstrapSpec(&core.Task{})
	require.NoError(t, err)
	require.Len(t, spec.Files, 1)
	require.Equal(t, ".claude/settings.local.json", spec.Files[0].Path)
	require.Equal(t, os.FileMode(0o644), spec.Files[0].FileMode)
	require.Contains(t, string(spec.Files[0].Content), "127.0.0.1:4000")
}

func TestRepositorySuggestTaskName_DelegatesToClaudeProposal(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		RunWithStdin(mock.Anything, execx.RunWithStdinOptions{
			Stdin: "add billing retry flow",
			Name:  "claude",
			Args: []string{
				"-p",
				"--output-format", "json",
				"--tools", "",
				"--system-prompt", prompts.SuggestTaskPrompt,
			},
		}).
		Return(execx.Result{
			Stdout: `{"type":"result","subtype":"success","result":"{\"branch_type\":\"feat\",\"name\":\"Billing Retry Flow\"}","is_error":false}` + "\n",
		}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "claude"})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "Billing Retry Flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositoryDetectRuntimeState_ReturnsNeedsInputForPrompt(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t), Config{Binary: "claude"})

	state := repo.DetectRuntimeState(core.RuntimeSnapshot{
		ForegroundCommand: "claude",
		Content:           "❯ add billing retry flow\n",
		ObservedAt:        time.Date(2026, 4, 6, 10, 0, 2, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 6, 10, 0, 1, 0, time.UTC),
	})
	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRepositoryProposeTaskName_ParsesJSONOutput(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		RunWithStdin(mock.Anything, execx.RunWithStdinOptions{
			Stdin: "add billing retry flow",
			Name:  "claude",
			Args: []string{
				"-p",
				"--output-format", "json",
				"--tools", "",
				"--system-prompt", prompts.SuggestTaskPrompt,
			},
		}).
		Return(execx.Result{
			Stdout: `{"type":"result","subtype":"success","cost_usd":0.002,"duration_ms":1500,"duration_api_ms":1200,"is_error":false,"num_turns":1,"result":"{\"branch_type\":\"feat\",\"name\":\"Billing Retry Flow\"}","session_id":"abc123","total_cost_usd":0.002}` + "\n",
		}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "claude"})

	suggestion, err := repo.ProposeTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "Billing Retry Flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositoryProposeTaskName_FallsBackToPlainStringResult(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		RunWithStdin(mock.Anything, execx.RunWithStdinOptions{
			Stdin: "switch sqlite to sqlc",
			Name:  "claude",
			Args: []string{
				"-p",
				"--output-format", "json",
				"--tools", "",
				"--system-prompt", prompts.SuggestTaskPrompt,
			},
		}).
		Return(execx.Result{
			Stdout: `{"type":"result","subtype":"success","result":"Migrate to ` + "`sqlc`" + `","is_error":false}` + "\n",
		}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "claude"})

	suggestion, err := repo.ProposeTaskName(t.Context(), "switch sqlite to sqlc")
	require.NoError(t, err)
	require.Equal(t, "Migrate to sqlc", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositoryProposeTaskName_ReturnsErrorOnEmptyResult(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		RunWithStdin(mock.Anything, execx.RunWithStdinOptions{
			Stdin: "do something",
			Name:  "claude",
			Args: []string{
				"-p",
				"--output-format", "json",
				"--tools", "",
				"--system-prompt", prompts.SuggestTaskPrompt,
			},
		}).
		Return(execx.Result{Stdout: `{"type":"result","subtype":"success","result":"","is_error":false}` + "\n"}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "claude"})

	_, err := repo.ProposeTaskName(t.Context(), "do something")
	require.Error(t, err)
}

func TestRepositoryProposeTaskName_ReturnsErrorOnAPIError(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		RunWithStdin(mock.Anything, execx.RunWithStdinOptions{
			Stdin: "do something",
			Name:  "claude",
			Args: []string{
				"-p",
				"--output-format", "json",
				"--tools", "",
				"--system-prompt", prompts.SuggestTaskPrompt,
			},
		}).
		Return(execx.Result{Stdout: `{"type":"result","subtype":"error","result":"API error","is_error":true}` + "\n"}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "claude"})

	_, err := repo.ProposeTaskName(t.Context(), "do something")
	require.Error(t, err)
}

func TestRepositoryIsAvailable_CallsClaudeVersion(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "claude", "--version").
		Return(execx.Result{Stdout: "1.0.0\n"}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "claude"})

	err := repo.IsAvailable(t.Context())
	require.NoError(t, err)
}
