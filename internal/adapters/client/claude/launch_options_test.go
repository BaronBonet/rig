package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/prompts"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryLaunchSpecs_PassTheTasksModelAndEffort(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	task := &core.Task{Prompt: "add billing retry flow", Model: "opus", Effort: "xhigh"}

	start, err := repo.BuildTaskSessionLaunchSpec(task)
	require.NoError(t, err)
	require.Equal(t, []string{"claude", "--model", "opus", "--effort", "xhigh"}, start.Command)
	require.Equal(t, []string{"add billing retry flow"}, start.PrefillInput)

	resume, err := repo.BuildReconnectTaskSessionLaunchSpec(task, "sess-1")
	require.NoError(t, err)
	require.Equal(t, []string{"claude", "--resume", "sess-1", "--model", "opus", "--effort", "xhigh"}, resume.Command,
		"a resumed session keeps the task's launch options")

	plain, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Model: "  "})
	require.NoError(t, err)
	require.Equal(t, []string{"claude"}, plain.Command, "blank options mean the provider's defaults")
}

func TestRepositoryLaunchOptions_ListsModelAliasesAndEffortsAndCanWriteHandoffs(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	options := repo.LaunchOptions()

	require.Equal(t, []string{"fable", "opus", "sonnet", "haiku"}, options.Models)
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, options.Efforts)
	require.True(t, options.Handoff)
	require.Equal(t, "/exit", repo.ExitCommand())
}

func TestRepositoryWriteSessionHandoff_ForksThePreviousSessionInPrintModeAndSavesTheNote(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo, dataDir := newTestRepository(t, runner)
	var opts subprocess.RunWithStdinOptions
	runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, o subprocess.RunWithStdinOptions) (subprocess.Result, error) {
			opts = o
			return subprocess.Result{Stdout: "# Handoff\n\n1. Goal: retries\n"}, nil
		},
	)

	path, err := repo.WriteSessionHandoff(context.Background(), &core.Task{
		ID:           "task-1",
		WorktreePath: "/tmp/repo-task",
		ProviderEnv:  core.ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-work"},
	}, core.TaskProviderSession{
		ProviderSessionID: "sess-1234567890",
		Model:             "claude-opus-5-5",
		Cwd:               "/tmp/repo-task/sub",
	}, "")

	require.NoError(t, err)
	require.Equal(t, "claude", opts.Name)
	require.Equal(t, []string{
		"-p", "--output-format", "text",
		"--resume", "sess-1234567890", "--fork-session", "--no-session-persistence",
		"--setting-sources", "user", "--tools", "",
		"--model", "claude-opus-5-5",
	}, opts.Args, "the session is forked, unsaved, without workspace hooks or tools, on its own model")
	require.Equal(t, prompts.HandoffPrompt, opts.Stdin,
		"the request goes in on stdin: as an argument the variadic --tools would swallow it")
	require.Equal(t, "/tmp/repo-task/sub", opts.Cwd, "the session is found where it was started")
	require.Equal(t, map[string]string{"CLAUDE_CONFIG_DIR": "/home/me/.claude-work"}, opts.Env)
	require.True(
		t,
		strings.HasPrefix(path, filepath.Join(dataDir, "handoffs", "task-1")+string(filepath.Separator)),
		path,
	)
	require.True(t, strings.HasSuffix(path, "-sess-123.md"), path)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "# Handoff\n\n1. Goal: retries\n", string(content))
}

func TestRepositoryWriteSessionHandoff_UsesTheTasksModelWhenTheSessionRecordedNone(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo, _ := newTestRepository(t, runner)
	var opts subprocess.RunWithStdinOptions
	runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, o subprocess.RunWithStdinOptions) (subprocess.Result, error) {
			opts = o
			return subprocess.Result{Stdout: "note"}, nil
		},
	)

	_, err := repo.WriteSessionHandoff(context.Background(), &core.Task{
		ID:           "task-1",
		WorktreePath: "/tmp/repo-task",
		Model:        "opus",
	}, core.TaskProviderSession{ProviderSessionID: "sess-1"}, "")

	require.NoError(t, err)
	require.Contains(t, strings.Join(opts.Args, " "), "--model opus")
	require.Equal(t, "/tmp/repo-task", opts.Cwd)
}

func TestRepositoryWriteSessionHandoff_TellsTheSessionWhatTheNextOneWillDo(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo, _ := newTestRepository(t, runner)
	var opts subprocess.RunWithStdinOptions
	runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, o subprocess.RunWithStdinOptions) (subprocess.Result, error) {
			opts = o
			return subprocess.Result{Stdout: "note"}, nil
		},
	)

	_, err := repo.WriteSessionHandoff(context.Background(), &core.Task{ID: "task-1"},
		core.TaskProviderSession{ProviderSessionID: "sess-1"}, "add the search page next")

	require.NoError(t, err)
	request := opts.Stdin
	require.True(t, strings.HasPrefix(request, prompts.HandoffPrompt), "the standard request comes first")
	require.Contains(
		t,
		request,
		"The next session will start with this instruction from the user:\nadd the search page next",
	)
}

func TestRepositoryWriteSessionHandoff_RejectsAnEmptyNote(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo, dataDir := newTestRepository(t, runner)
	runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).Return(subprocess.Result{Stdout: "  \n"}, nil)

	_, err := repo.WriteSessionHandoff(context.Background(), &core.Task{ID: "task-1"},
		core.TaskProviderSession{ProviderSessionID: "sess-1"}, "")

	require.ErrorContains(t, err, "empty handoff note")
	_, statErr := os.Stat(filepath.Join(dataDir, "handoffs"))
	require.True(t, os.IsNotExist(statErr), "nothing is written for an empty note")
}
