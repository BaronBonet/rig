package tmuxsession

import (
	"context"
	"errors"
	"testing"
	"time"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryStartTaskSession_LaunchesCommandAndPrefillsInputWithoutSubmitting(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	var slept time.Duration
	repo.sleep = func(d time.Duration) { slept = d }

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "agent", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:agent", "codex", "C-m",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "›"}, nil,
			"capture-pane", "-t", "=repo_task:agent", "-p",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:agent", "fix billing retry flow",
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
	require.Equal(t, promptSubmitDelay, slept)
}

func TestRepositoryStartTaskSession_CleansUpSessionWhenEditorWindowCreationFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo-billing-retry-flow", "-n", "agent", "-c", "/tmp/repo-billing-retry-flow",
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

func expectTmuxRun(runner *subprocess.MockRunner, result subprocess.Result, err error, args ...string) *mock.Call {
	callArgs := make([]interface{}, 0, len(args)+3)
	callArgs = append(callArgs, mock.Anything, "", "tmux")
	for _, arg := range args {
		callArgs = append(callArgs, arg)
	}
	return runner.On("Run", callArgs...).Return(result, err).Once()
}
