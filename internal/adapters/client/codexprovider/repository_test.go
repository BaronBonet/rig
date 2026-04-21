package codexprovider

import (
	"context"
	"os"
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildTaskSessionLaunchSpec_StartsCodexAndPrefillsTaskPrompt(t *testing.T) {
	repo := New(stubRunner{}, Config{Binary: "codex"}, HookForwardingConfig{})

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryBuildWorkspaceBootstrapSpec_RendersCodexHooksAndForwarderScript(t *testing.T) {
	repo := New(stubRunner{}, Config{
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
	require.NotContains(t, string(spec.Files[0].Content), `"PermissionRequest"`)
	require.Equal(t, ".codex/hooks/forward-to-rig.sh", spec.Files[1].Path)
	require.Equal(t, os.FileMode(0o755), spec.Files[1].FileMode)
	require.Contains(t, string(spec.Files[1].Content), "/tmp/rig-bin")
	require.Contains(t, string(spec.Files[1].Content), "/tmp/source")
	require.Contains(t, string(spec.Files[1].Content), "http://127.0.0.1:4124/codex-hook")
	require.NotContains(t, string(spec.Files[1].Content), "status-ingest")
}

func TestRepositorySuggestTaskName_DelegatesToCodexProposal(t *testing.T) {
	runner := stubRunner{
		runFn: func(_ context.Context, cwd string, name string, args ...string) (subprocess.Result, error) {
			require.Equal(t, "", cwd)
			require.Equal(t, "codex", name)
			require.Equal(
				t,
				[]string{"exec", "--skip-git-repo-check", "--output-last-message", args[3], args[4]},
				args,
			)
			return subprocess.Result{Stdout: "billing retry flow\n"}, nil
		},
	}
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositorySuggestTaskName_PrefersOutputFileOverStdout(t *testing.T) {
	runner := stubRunner{
		runFn: func(_ context.Context, _ string, _ string, args ...string) (subprocess.Result, error) {
			require.NoError(
				t,
				os.WriteFile(args[3], []byte("{\"name\":\"File Result\",\"branch_type\":\"feat\"}\n"), 0o600),
			)
			return subprocess.Result{Stdout: "stdout result\n"}, nil
		},
	}
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "File Result", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

type stubRunner struct {
	runFn          func(context.Context, string, string, ...string) (subprocess.Result, error)
	runWithStdinFn func(context.Context, subprocess.RunWithStdinOptions) (subprocess.Result, error)
}

func (s stubRunner) Run(ctx context.Context, cwd string, name string, args ...string) (subprocess.Result, error) {
	if s.runFn == nil {
		return subprocess.Result{}, nil
	}
	return s.runFn(ctx, cwd, name, args...)
}

func (s stubRunner) RunWithStdin(ctx context.Context, opts subprocess.RunWithStdinOptions) (subprocess.Result, error) {
	if s.runWithStdinFn == nil {
		return subprocess.Result{}, nil
	}
	return s.runWithStdinFn(ctx, opts)
}
