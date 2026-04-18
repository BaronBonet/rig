package codexagent

import (
	"context"
	"os"
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/execx"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildTaskSessionLaunchSpec_StartsCodexAndPrefillsTaskPrompt(t *testing.T) {
	repo := New(execx.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{})

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryBuildWorkspaceBootstrapSpec_RendersCodexHooksAndForwarderScript(t *testing.T) {
	repo := New(execx.NewMockRunner(t), Config{
		Binary: "codex",
	}, HookForwardingConfig{
		RigBinaryPath: "/tmp/rig-bin",
		SourceRoot:    "/tmp/source",
	})

	spec, err := repo.BuildWorkspaceBootstrapSpec(&core.Task{})
	require.NoError(t, err)
	require.Len(t, spec.Files, 2)
	require.Equal(t, ".codex/hooks.json", spec.Files[0].Path)
	require.Equal(t, os.FileMode(0o644), spec.Files[0].FileMode)
	require.Contains(t, string(spec.Files[0].Content), `"SessionStart"`)
	require.Contains(t, string(spec.Files[0].Content), `"PermissionRequest"`)
	require.Equal(t, ".codex/hooks/forward-to-rig.sh", spec.Files[1].Path)
	require.Equal(t, os.FileMode(0o755), spec.Files[1].FileMode)
	require.Contains(t, string(spec.Files[1].Content), "/tmp/rig-bin")
	require.Contains(t, string(spec.Files[1].Content), "/tmp/source")
	require.Contains(t, string(spec.Files[1].Content), "RIG_DEBUG_MODE=status-ingest")
}

func TestRepositorySuggestTaskName_DelegatesToCodexProposal(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "codex", "exec", "--skip-git-repo-check", "--output-last-message", mock.Anything, mock.Anything).
		Return(execx.Result{Stdout: "billing retry flow\n"}, nil).
		Once()
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositorySuggestTaskName_PrefersOutputFileOverStdout(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "codex", "exec", "--skip-git-repo-check", "--output-last-message", mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, _ string, args ...string) (execx.Result, error) {
			require.NoError(t, os.WriteFile(args[3], []byte("{\"name\":\"File Result\",\"branch_type\":\"feat\"}\n"), 0o600))
			return execx.Result{Stdout: "stdout result\n"}, nil
		}).
		Once()
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "File Result", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}
