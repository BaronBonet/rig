package tmux

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryStartTaskSession_LaunchesCommandAndPrefillsInputWithoutSubmitting(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	var slept []time.Duration
	repo.sleep = func(d time.Duration) { slept = append(slept, d) }

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:task", "codex", "C-m",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "›"}, nil,
			"capture-pane", "-t", "=repo_task:task", "-p",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-l", "-t", "=repo_task:task", "--", "fix billing retry flow",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.NoError(t, err)
	require.Equal(t, []time.Duration{promptSubmitDelay, promptInputSettleDelay}, slept)
}

func TestRepositoryStartTaskSession_PrefillsLargeInputInLiteralChunks(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	repo.sleep = func(time.Duration) {}

	prompt := strings.Repeat("debug output\n", 5000)

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:task", "codex", "C-m",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "›"}, nil,
			"capture-pane", "-t", "=repo_task:task", "-p",
		),
	)
	for _, chunk := range splitTextChunks(prompt, promptInputChunkSize) {
		expectTmuxRun(runner, subprocess.Result{}, nil, "send-keys", "-l", "-t", "=repo_task:task", "--", chunk)
	}

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{prompt},
	})

	require.NoError(t, err)
}

func TestRepositoryStartTaskSession_LeavesShellIdleWhenLaunchCommandIsEmpty(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{})

	require.NoError(t, err)
}

func TestRepositoryStartTaskSession_CleansUpSessionWhenEditorWindowCreationFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo-billing-retry-flow", "-n", "task", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, subprocess.Result{}, errors.New("new-window failed"),
			"new-window", "-d", "-t", "=repo-billing-retry-flow", "-n", "editor", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"kill-session", "-t", "=repo-billing-retry-flow",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo-billing-retry-flow",
		WorktreePath: "/tmp/repo-billing-retry-flow",
	}, core.TaskSessionLaunchSpec{
		Command: []string{"codex"},
	})

	require.EqualError(t, err, "new-window failed")
}

func TestRepositoryStartTaskSession_CleansUpSessionWhenPrefillFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:task", "codex", "C-m",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "›"}, nil,
			"capture-pane", "-t", "=repo_task:task", "-p",
		),
		expectTmuxRun(runner, subprocess.Result{}, errors.New("send-keys failed"),
			"send-keys", "-l", "-t", "=repo_task:task", "--", "fix billing retry flow",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"kill-session", "-t", "=repo_task",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.EqualError(t, err, "send-keys failed")
}

func TestRepositoryAttachTaskSession_SwitchesClientWhenInsideTmux(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.getenv = func(key string) string {
		if key == "TMUX" {
			return "/tmp/tmux-1000/default,123,0"
		}
		return ""
	}

	expectTmuxRun(runner, subprocess.Result{}, nil, "switch-client", "-t", "=repo_task")

	err := repo.AttachTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.NoError(t, err)
}

func TestRepositoryAttachTaskSession_AttachesWhenOutsideTmux(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.getenv = func(string) string { return "" }

	expectTmuxRun(runner, subprocess.Result{}, nil, "attach-session", "-t", "=repo_task")

	err := repo.AttachTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.NoError(t, err)
}

func TestRepositoryAttachTaskSession_ReturnsErrTaskSessionNotFoundWhenSessionIsMissing(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.getenv = func(string) string { return "" }

	expectTmuxRun(
		runner,
		subprocess.Result{Stderr: "can't find session: repo_task"},
		subprocess.CommandError{
			Name:   "tmux",
			Args:   []string{"attach-session", "-t", "=repo_task"},
			Stderr: "can't find session: repo_task",
			Err:    errors.New("exit status 1"),
		},
		"attach-session",
		"-t",
		"=repo_task",
	)

	err := repo.AttachTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.ErrorIs(t, err, core.ErrTaskSessionNotFound)
}

func TestRepositoryInspectTaskSession_ReturnsActiveTaskWindowCommands(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(
		runner,
		subprocess.Result{Stdout: "zsh\ncodex\n"},
		nil,
		"list-panes",
		"-t",
		"=repo_task:task",
		"-F",
		"#{pane_current_command}",
	)

	state, err := repo.InspectTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.NoError(t, err)
	require.True(t, state.Exists)
	require.Equal(t, []string{"zsh", "codex"}, state.ActiveCommands)
}

func TestRepositoryInspectTaskSession_ReturnsMissingWhenTaskWindowIsGone(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(
		runner,
		subprocess.Result{Stderr: "can't find window: task"},
		subprocess.CommandError{
			Name:   "tmux",
			Args:   []string{"list-panes", "-t", "=repo_task:task", "-F", "#{pane_current_command}"},
			Stderr: "can't find window: task",
			Err:    errors.New("exit status 1"),
		},
		"list-panes",
		"-t",
		"=repo_task:task",
		"-F",
		"#{pane_current_command}",
	)

	state, err := repo.InspectTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.NoError(t, err)
	require.False(t, state.Exists)
	require.Empty(t, state.ActiveCommands)
}

func TestRepositoryDeleteTaskSession_KillsSession(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(runner, subprocess.Result{}, nil, "kill-session", "-t", "=repo-billing-retry-flow")

	err := repo.DeleteTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo-billing-retry-flow",
	})

	require.NoError(t, err)
}

func TestRepositoryDeleteTaskSession_IgnoresMissingSession(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(
		runner,
		subprocess.Result{Stderr: "can't find session: repo-billing-retry-flow"},
		subprocess.CommandError{
			Name:   "tmux",
			Args:   []string{"kill-session", "-t", "=repo-billing-retry-flow"},
			Stderr: "can't find session: repo-billing-retry-flow",
			Err:    errors.New("exit status 1"),
		},
		"kill-session",
		"-t",
		"=repo-billing-retry-flow",
	)

	err := repo.DeleteTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo-billing-retry-flow",
	})

	require.NoError(t, err)
}

func expectTmuxRun(runner *subprocess.MockRunner, result subprocess.Result, err error, args ...string) *mock.Call {
	callArgs := make([]interface{}, 0, len(args)+3)
	callArgs = append(callArgs, mock.Anything, "", "tmux")
	for _, arg := range args {
		callArgs = append(callArgs, arg)
	}
	return runner.On("Run", callArgs...).Return(result, err).Once()
}
