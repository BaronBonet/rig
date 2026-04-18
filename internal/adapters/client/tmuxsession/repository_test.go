package tmuxsession

import (
	"context"
	"errors"
	"testing"
	"time"

	"rig/internal/core"
	"rig/internal/pkg/execx"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryStartTaskSession_LaunchesCommandAndTypesInitialInput(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := newRepository(runner)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, execx.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "agent", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, execx.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, execx.Result{}, nil,
			"send-keys", "-t", "=repo_task:agent", "codex", "C-m",
		),
		expectTmuxRun(runner, execx.Result{Stdout: "›"}, nil,
			"capture-pane", "-t", "=repo_task:agent", "-p",
		),
		expectTmuxRun(runner, execx.Result{}, nil,
			"send-keys", "-t", "=repo_task:agent", "fix billing retry flow",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		InitialInput: []string{"fix billing retry flow"},
	})

	require.NoError(t, err)
}

func TestRepositoryStartTaskSession_CleansUpSessionWhenEditorWindowCreationFails(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := newRepository(runner)

	mock.InOrder(
		expectTmuxRun(runner, execx.Result{}, nil,
			"new-session", "-d", "-s", "repo-billing-retry-flow", "-n", "agent", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, execx.Result{}, errors.New("new-window failed"),
			"new-window", "-d", "-t", "=repo-billing-retry-flow", "-n", "editor", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, execx.Result{}, nil,
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

func expectTmuxRun(runner *execx.MockRunner, result execx.Result, err error, args ...string) *mock.Call {
	callArgs := make([]interface{}, 0, len(args)+3)
	callArgs = append(callArgs, mock.Anything, "", "tmux")
	for _, arg := range args {
		callArgs = append(callArgs, arg)
	}
	return runner.On("Run", callArgs...).Return(result, err).Once()
}
