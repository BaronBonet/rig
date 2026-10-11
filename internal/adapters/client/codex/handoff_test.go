package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/prompts"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

func newHandoffRepository(t *testing.T, runner subprocess.Runner) (*repository, string) {
	t.Helper()
	dataDir := t.TempDir()
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.rigDataDir = func() (string, error) { return dataDir, nil }
	return repo, dataDir
}

// outputFlagValue is where the command asks Codex to write its last message.
func outputFlagValue(t *testing.T, args []string) string {
	t.Helper()
	index := slices.Index(args, "-o")
	require.GreaterOrEqual(t, index, 0, "the command names an output file")
	require.Less(t, index+1, len(args))
	return args[index+1]
}

func TestRepositoryWriteSessionHandoff_ForksTheSessionWithoutSavingAndKeepsTheNote(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	var got subprocess.RunWithStdinOptions
	runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, opts subprocess.RunWithStdinOptions) (subprocess.Result, error) {
			got = opts
			note := "## Goal\nShip the billing retry flow."
			require.NoError(t, os.WriteFile(outputFlagValue(t, opts.Args), []byte(note+"\n\n"), 0o600))
			return subprocess.Result{Stdout: "progress chatter, not the note"}, nil
		},
	).Once()
	repo, dataDir := newHandoffRepository(t, runner)
	task := &core.Task{
		ID:           "task-1",
		WorktreePath: "/work/task-1",
		Model:        "gpt-6-luna",
		Effort:       "high",
		ProviderEnv:  core.ProviderEnv{"CODEX_HOME": "/home/me/.codex-client"},
	}
	session := core.TaskProviderSession{
		ProviderSessionID: "0199aa-session",
		Model:             "gpt-6-sol",
		Cwd:               "/work/task-1/api",
	}

	path, err := repo.WriteSessionHandoff(t.Context(), task, session, "add the retry metrics")

	require.NoError(t, err)
	output := outputFlagValue(t, got.Args)
	require.Equal(t, []string{
		"exec", "fork", "0199aa-session", "--ephemeral", "--disable", "hooks", "--skip-git-repo-check",
		"-c", `sandbox_mode="read-only"`, "-o", output,
		"-m", "gpt-6-sol", "-c", "model_reasoning_effort=high", "-",
	}, got.Args, "a fork that is never saved, so the session is left as it was")
	require.Equal(t, "codex", got.Name)
	require.Equal(t, "/work/task-1/api", got.Cwd, "the session's own folder")
	require.Equal(t, map[string]string{"CODEX_HOME": "/home/me/.codex-client"}, got.Env, "the task's account")
	require.True(t, strings.HasPrefix(got.Stdin, prompts.HandoffPrompt))
	require.Contains(t, got.Stdin, "add the retry metrics", "the session is told what the next one will do")

	require.Equal(t, filepath.Join(dataDir, "handoffs", "task-1"), filepath.Dir(path))
	require.True(t, strings.HasSuffix(path, "-0199aa-s.md"))
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "## Goal\nShip the billing retry flow.\n", string(saved))
	_, err = os.Stat(output)
	require.True(t, os.IsNotExist(err), "the temporary output file is removed")
}

func TestRepositoryWriteSessionHandoff_FallsBackToTheTasksModelInItsWorktree(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	var got subprocess.RunWithStdinOptions
	runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, opts subprocess.RunWithStdinOptions) (subprocess.Result, error) {
			got = opts
			require.NoError(t, os.WriteFile(outputFlagValue(t, opts.Args), []byte("note"), 0o600))
			return subprocess.Result{}, nil
		},
	).Once()
	repo, _ := newHandoffRepository(t, runner)
	task := &core.Task{ID: "task-1", WorktreePath: "/work/task-1", Model: "gpt-6-luna"}

	_, err := repo.WriteSessionHandoff(t.Context(), task, core.TaskProviderSession{ProviderSessionID: "sess-1"}, "")

	require.NoError(t, err)
	require.Equal(t, "/work/task-1", got.Cwd)
	require.Equal(t, []string{"-m", "gpt-6-luna", "-"}, got.Args[len(got.Args)-3:])
	require.NotContains(t, strings.Join(got.Args, " "), "model_reasoning_effort",
		"without an effort of its own the session's config decides")
	require.Equal(t, prompts.HandoffPrompt, got.Stdin)
}

func TestRepositoryWriteSessionHandoff_FailsWithoutANote(t *testing.T) {
	for name, tc := range map[string]struct {
		session core.TaskProviderSession
		run     func(opts subprocess.RunWithStdinOptions) error
		wantErr string
	}{
		"no session ID": {
			session: core.TaskProviderSession{ProviderSessionID: " "},
			wantErr: "session ID is required",
		},
		"codex fails": {
			session: core.TaskProviderSession{ProviderSessionID: "sess-1"},
			run:     func(subprocess.RunWithStdinOptions) error { return errors.New("not logged in") },
			wantErr: "codex exec fork failed: not logged in",
		},
		"empty note": {
			session: core.TaskProviderSession{ProviderSessionID: "sess-1"},
			run: func(opts subprocess.RunWithStdinOptions) error {
				return os.WriteFile(outputFlagValue(t, opts.Args), []byte(" \n"), 0o600)
			},
			wantErr: "codex returned an empty handoff note",
		},
	} {
		t.Run(name, func(t *testing.T) {
			runner := subprocess.NewMockRunner(t)
			if tc.run != nil {
				runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).RunAndReturn(
					func(_ context.Context, opts subprocess.RunWithStdinOptions) (subprocess.Result, error) {
						return subprocess.Result{}, tc.run(opts)
					},
				).Once()
			}
			repo, dataDir := newHandoffRepository(t, runner)

			_, err := repo.WriteSessionHandoff(t.Context(), &core.Task{ID: "task-1"}, tc.session, "")

			require.ErrorContains(t, err, tc.wantErr)
			_, statErr := os.Stat(filepath.Join(dataDir, "handoffs"))
			require.True(t, os.IsNotExist(statErr), "no note is saved")
		})
	}
}
