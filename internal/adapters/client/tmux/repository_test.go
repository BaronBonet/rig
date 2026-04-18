package tmux

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

func TestRepository_StartTaskSession_LaunchesCommandAndTypesPrefillInput(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
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
		TmuxSession:      "repo_task",
		WorktreePath:     "/tmp/repo-task",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	}, core.LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.NoError(t, err)
}

func TestRepositoryCreateSession_UsesDetachedSessionInWorkingDir(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)

	mock.InOrder(
		expectTmuxRun(runner, execx.Result{}, nil,
			"new-session", "-d", "-s", "repo-billing-retry-flow", "-n", "agent", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, execx.Result{}, nil,
			"new-window", "-d", "-t", "=repo-billing-retry-flow", "-n", "editor", "-c", "/tmp/repo-billing-retry-flow",
		),
	)

	err := repo.CreateSession(context.Background(), core.CreateSessionInput{
		SessionName:      "repo-billing-retry-flow",
		WorkingDir:       "/tmp/repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})
	require.NoError(t, err)
}

func TestRepositoryCreateSession_KillsSessionIfEditorWindowCreationFails(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)

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

	err := repo.CreateSession(context.Background(), core.CreateSessionInput{
		SessionName:      "repo-billing-retry-flow",
		WorkingDir:       "/tmp/repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})

	require.EqualError(t, err, "new-window failed")
}

func TestRepositoryCreateSession_JoinsWindowCreationAndCleanupErrors(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)

	mock.InOrder(
		expectTmuxRun(runner, execx.Result{}, nil,
			"new-session", "-d", "-s", "repo-billing-retry-flow", "-n", "agent", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, execx.Result{}, errors.New("new-window failed"),
			"new-window", "-d", "-t", "=repo-billing-retry-flow", "-n", "editor", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, execx.Result{}, errors.New("kill-session failed"),
			"kill-session", "-t", "=repo-billing-retry-flow",
		),
	)

	err := repo.CreateSession(context.Background(), core.CreateSessionInput{
		SessionName:      "repo-billing-retry-flow",
		WorkingDir:       "/tmp/repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "new-window failed")
	require.ErrorContains(t, err, "kill-session failed")
}

func TestRepositoryOpenTaskSession_UsesTaskSession(t *testing.T) {
	t.Setenv("TMUX", "")

	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil, "attach-session", "-t", "=repo-billing-retry-flow")

	err := repo.OpenTaskSession(context.Background(), &core.Task{TmuxSession: "repo-billing-retry-flow"})
	require.NoError(t, err)
}

func TestRepositoryDeleteTaskSession_UsesTaskSession(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil, "kill-session", "-t", "=repo-billing-retry-flow")

	err := repo.DeleteTaskSession(context.Background(), &core.Task{TmuxSession: "repo-billing-retry-flow"})
	require.NoError(t, err)
}

func TestRepositoryInspectTaskSession_ReturnsSessionAndWindowResources(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)

	mock.InOrder(
		expectTmuxRun(runner, execx.Result{}, nil, "has-session", "-t", "=repo-billing-retry-flow"),
		expectTmuxRun(runner, execx.Result{Stdout: "agent\neditor\n"}, nil,
			"list-windows", "-t", "=repo-billing-retry-flow", "-F", "#{window_name}",
		),
		expectTmuxRun(runner, execx.Result{Stdout: "agent\neditor\n"}, nil,
			"list-windows", "-t", "=repo-billing-retry-flow", "-F", "#{window_name}",
		),
	)

	resources, err := repo.InspectTaskSession(context.Background(), &core.Task{
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})
	require.NoError(t, err)
	require.Equal(t, core.SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}, resources)
}

func TestRepositorySnapshotTaskSession_UsesRuntimeMonitor(t *testing.T) {
	repo := NewRepository(execx.NewMockRunner(t))
	runtimeMonitor := core.NewMockRuntimeMonitor(t)
	repo.runtimeMonitor = runtimeMonitor

	task := &core.Task{TmuxSession: "repo-billing-retry-flow"}
	runtimeMonitor.EXPECT().Snapshot(mock.Anything, task).Return(core.RuntimeSnapshot{
		SessionName: "repo-billing-retry-flow",
		PaneID:      "%24",
	}, nil).Once()

	snapshot, err := repo.SnapshotTaskSession(context.Background(), task)
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", snapshot.SessionName)
	require.Equal(t, "%24", snapshot.PaneID)
}

func TestRepositoryNormalizesColonSessionNamesAcrossTmuxCommands(t *testing.T) {
	t.Setenv("TMUX", "")

	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)

	mock.InOrder(
		expectTmuxRun(runner, execx.Result{}, nil,
			"new-session", "-d", "-s", "repo-billing-retry-flow", "-n", "agent", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, execx.Result{}, nil,
			"new-window", "-d", "-t", "=repo-billing-retry-flow", "-n", "editor", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, execx.Result{}, nil, "has-session", "-t", "=repo-billing-retry-flow"),
		expectTmuxRun(runner, execx.Result{Stdout: "agent\neditor\n"}, nil,
			"list-windows", "-t", "=repo-billing-retry-flow", "-F", "#{window_name}",
		),
		expectTmuxRun(runner, execx.Result{}, nil, "attach-session", "-t", "=repo-billing-retry-flow"),
		expectTmuxRun(runner, execx.Result{}, nil,
			"send-keys", "-t", "=repo-billing-retry-flow:editor", "codex 'fix bug'", "C-m",
		),
		expectTmuxRun(runner, execx.Result{}, nil, "kill-session", "-t", "=repo-billing-retry-flow"),
	)

	input := core.CreateSessionInput{
		SessionName:      "repo:billing-retry-flow",
		WorkingDir:       "/tmp/repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	}

	require.NoError(t, repo.CreateSession(context.Background(), input))

	exists, err := repo.SessionExists(context.Background(), input.SessionName)
	require.NoError(t, err)
	require.True(t, exists)

	exists, err = repo.WindowExists(context.Background(), input.SessionName, "editor")
	require.NoError(t, err)
	require.True(t, exists)

	require.NoError(t, repo.AttachOrSwitch(context.Background(), input.SessionName))
	require.NoError(
		t,
		repo.SendKeysToWindow(context.Background(), input.SessionName, "editor", []string{"codex", "fix bug"}),
	)
	require.NoError(t, repo.KillSession(context.Background(), input.SessionName))
}

func TestRepositoryNormalizesColonSessionNamesForSwitchClientInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-123/default,123,0")

	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil, "switch-client", "-t", "=repo-billing-retry-flow")

	require.NoError(t, repo.AttachOrSwitch(context.Background(), "repo:billing-retry-flow"))
}

func TestRepositorySendKeysToWindow_UsesNamedWindowTarget(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil,
		"send-keys", "-t", "=repo-billing-retry-flow:editor", "codex 'fix bug'", "C-m",
	)

	err := repo.SendKeysToWindow(
		context.Background(),
		"repo-billing-retry-flow",
		"editor",
		[]string{"codex", "fix bug"},
	)
	require.NoError(t, err)
}

func TestRepositorySendKeysToWindow_DefaultsEmptyWindowToAgent(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil,
		"send-keys", "-t", "=repo-billing-retry-flow:agent", "codex 'fix bug'", "C-m",
	)

	err := repo.SendKeysToWindow(context.Background(), "repo-billing-retry-flow", "", []string{"codex", "fix bug"})
	require.NoError(t, err)
}

func TestRepositoryTypeInWindow_SendsKeysWithoutEnter(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil,
		"send-keys", "-t", "=repo-billing-retry-flow:agent", "codex fix bug",
	)

	err := repo.TypeInWindow(
		context.Background(),
		"repo-billing-retry-flow",
		"agent",
		[]string{"codex", "fix bug"},
	)
	require.NoError(t, err)
}

func TestRepositoryAttachOrSwitch_UsesExactSessionTarget(t *testing.T) {
	t.Setenv("TMUX", "")

	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil, "attach-session", "-t", "=repo-billing-retry-flow")

	err := repo.AttachOrSwitch(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
}

func TestRepositoryAttachOrSwitch_UsesSwitchClientInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-123/default,123,0")

	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil, "switch-client", "-t", "=repo-billing-retry-flow")

	err := repo.AttachOrSwitch(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
}

func TestRepositorySessionExists_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil, "has-session", "-t", "=repo-billing-retry-flow")

	exists, err := repo.SessionExists(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.True(t, exists)
}

func TestRepositorySessionExists_ReturnsFalseForMissingSessionOnly(t *testing.T) {
	runner := execx.NewMockRunner(t)
	expectTmuxRun(runner, execx.Result{Stderr: "can't find session: repo-billing-retry-flow\n"}, execx.CommandError{
		Name:   "tmux",
		Args:   []string{"has-session", "-t", "=repo-billing-retry-flow"},
		Stderr: "can't find session: repo-billing-retry-flow\n",
		Err:    errors.New("exit status 1"),
	}, "has-session", "-t", "=repo-billing-retry-flow")
	repo := NewRepository(runner)

	exists, err := repo.SessionExists(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestRepositorySessionExists_ReturnsErrorForTmuxFailure(t *testing.T) {
	runner := execx.NewMockRunner(t)
	expectTmuxRun(runner, execx.Result{Stderr: "failed to connect to server\n"}, execx.CommandError{
		Name:   "tmux",
		Args:   []string{"has-session", "-t", "=repo-billing-retry-flow"},
		Stderr: "failed to connect to server\n",
		Err:    errors.New("exit status 1"),
	}, "has-session", "-t", "=repo-billing-retry-flow")
	repo := NewRepository(runner)

	exists, err := repo.SessionExists(context.Background(), "repo-billing-retry-flow")
	require.Error(t, err)
	require.False(t, exists)
}

func TestRepositoryWindowExists_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{Stdout: "agent\neditor\n"}, nil,
		"list-windows", "-t", "=repo-billing-retry-flow", "-F", "#{window_name}",
	)

	exists, err := repo.WindowExists(context.Background(), "repo-billing-retry-flow", "agent")
	require.NoError(t, err)
	require.True(t, exists)
}

func TestRepositoryWindowExists_ReturnsFalseForMissingSessionOnly(t *testing.T) {
	runner := execx.NewMockRunner(t)
	expectTmuxRun(runner, execx.Result{Stderr: "can't find session: repo-billing-retry-flow\n"}, execx.CommandError{
		Name:   "tmux",
		Args:   []string{"list-windows", "-t", "=repo-billing-retry-flow", "-F", "#{window_name}"},
		Stderr: "can't find session: repo-billing-retry-flow\n",
		Err:    errors.New("exit status 1"),
	}, "list-windows", "-t", "=repo-billing-retry-flow", "-F", "#{window_name}")
	repo := NewRepository(runner)

	exists, err := repo.WindowExists(context.Background(), "repo-billing-retry-flow", "agent")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestRepositoryKillSession_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewMockRunner(t)
	repo := NewRepository(runner)
	expectTmuxRun(runner, execx.Result{}, nil, "kill-session", "-t", "=repo-billing-retry-flow")

	err := repo.KillSession(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
}

func expectTmuxRun(runner *execx.MockRunner, result execx.Result, err error, args ...string) *mock.Call {
	callArgs := make([]interface{}, 0, len(args)+3)
	callArgs = append(callArgs, mock.Anything, "", "tmux")
	for _, arg := range args {
		callArgs = append(callArgs, arg)
	}
	return runner.On("Run", callArgs...).Return(result, err).Once()
}
