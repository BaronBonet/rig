package codex

import (
	"testing"
	"time"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildLaunchCommand_IncludesPrompt(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t), Config{Binary: "codex"})

	cmd, err := repo.BuildLaunchCommand(&core.Task{
		Prompt: "add billing retry flow",
	})
	require.NoError(t, err)
	require.Equal(t, "codex", cmd[0])
	require.Equal(t, "add billing retry flow", cmd[len(cmd)-1])
}

func TestRepositoryLaunchRequest_UsesBinaryPromptAndTaskPrompt(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t), Config{Binary: "codex"})

	launch, err := repo.LaunchRequest(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositorySuggestTaskName_DelegatesToCodexProposal(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "codex", "exec", "--skip-git-repo-check", "--output-last-message", mock.Anything, mock.Anything).
		Return(execx.Result{Stdout: "billing retry flow\n"}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "codex"})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositoryDetectRuntimeState_ReturnsNeedsInputForPrompt(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t), Config{Binary: "codex"})

	state := repo.DetectRuntimeState(core.RuntimeSnapshot{
		ForegroundCommand: "codex",
		Content:           "› add billing retry flow\n",
		ObservedAt:        time.Date(2026, 4, 6, 10, 0, 2, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 6, 10, 0, 1, 0, time.UTC),
	})
	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRepositoryProposeTaskName_TrimsRunnerOutput(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "codex", "exec", "--skip-git-repo-check", "--output-last-message", mock.Anything, mock.Anything).
		Return(execx.Result{Stdout: "billing retry flow\n"}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "codex"})

	suggestion, err := repo.ProposeTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositoryProposeTaskName_ExtractsFinalTitleFromTranscriptOutput(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "codex", "exec", "--skip-git-repo-check", "--output-last-message", mock.Anything, mock.Anything).
		Return(execx.Result{Stdout: `OpenAI Codex v0.118.0 (research preview)
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
			nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "codex"})

	suggestion, err := repo.ProposeTaskName(t.Context(), "i want you to switch the sqlite repo to use sqlc")
	require.NoError(t, err)
	require.Equal(t, "Migrate SQLite Repo to sqlc", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositoryProposeTaskName_StripsMarkdownTicksFromTitle(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "codex", "exec", "--skip-git-repo-check", "--output-last-message", mock.Anything, mock.Anything).
		Return(execx.Result{Stdout: "Migrate SQLite Repo to `sqlc`\n"}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "codex"})

	suggestion, err := repo.ProposeTaskName(t.Context(), "switch the sqlite repo to use sqlc")
	require.NoError(t, err)
	require.Equal(t, "Migrate SQLite Repo to sqlc", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestExtractCodexTitle_ReturnsLastUsefulLine(t *testing.T) {
	raw := "OpenAI Codex v0.118.0\n--------\nuser: prompt\nbilling retry flow\nexit status 1\n"

	require.Equal(t, "billing retry flow", extractCodexTitle(raw))
}
