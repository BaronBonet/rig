package tmux

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task:",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "%4\t/private/tmp/tmux-501/default\t4722\n"}, nil,
			"new-session", "-d", "-P", "-F", paneRefFormat, "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
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
		expectTmuxRunWithStdin(runner, subprocess.RunWithStdinOptions{
			Cwd:   "",
			Name:  "tmux",
			Args:  []string{"load-buffer", "-b", "rig-prefill-repo_task-task", "-"},
			Stdin: "fix billing retry flow",
		}, subprocess.Result{}, nil),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"paste-buffer", "-p", "-t", "=repo_task:task", "-b", "rig-prefill-repo_task-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"delete-buffer", "-b", "rig-prefill-repo_task-task",
		),
	)

	pane, err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.NoError(t, err)
	require.Equal(t, core.TmuxPaneRef{
		Server: core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722},
		ID:     "%4",
	}, pane)
	require.Equal(t, []time.Duration{promptSubmitDelay, promptInputSettleDelay}, slept)
}

func TestRepositoryStartTaskSession_PrefillsLargeInputThroughTmuxBuffer(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	repo.sleep = func(time.Duration) {}

	prompt := strings.Repeat("debug output\n", 5000)

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task:",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-P", "-F", paneRefFormat, "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
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
		expectTmuxRunWithStdin(runner, subprocess.RunWithStdinOptions{
			Cwd:   "",
			Name:  "tmux",
			Args:  []string{"load-buffer", "-b", "rig-prefill-repo_task-task", "-"},
			Stdin: prompt,
		}, subprocess.Result{}, nil),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"paste-buffer", "-p", "-t", "=repo_task:task", "-b", "rig-prefill-repo_task-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"delete-buffer", "-b", "rig-prefill-repo_task-task",
		),
	)

	_, err := repo.StartTaskSession(context.Background(), &core.Task{
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
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task:",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-P", "-F", paneRefFormat, "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
	)

	pane, err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{})

	require.NoError(t, err)
	// tmux printed nothing for the new pane, so it stays unknown.
	require.Zero(t, pane)
}

func TestRepositoryStartTaskSession_CleansUpSessionWhenEditorWindowCreationFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo-billing-retry-flow:",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-P", "-F", paneRefFormat,
			"-s", "repo-billing-retry-flow", "-n", "task", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, subprocess.Result{}, errors.New("new-window failed"),
			"new-window", "-d", "-t", "=repo-billing-retry-flow", "-n", "editor", "-c", "/tmp/repo-billing-retry-flow",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"kill-session", "-t", "=repo-billing-retry-flow",
		),
	)

	_, err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo-billing-retry-flow",
		WorktreePath: "/tmp/repo-billing-retry-flow",
	}, core.TaskSessionLaunchSpec{
		Command: []string{"codex"},
	})

	require.EqualError(t, err, "new-window failed")
}

func TestRepositoryStartTaskSession_CleansUpSessionEvenWhenTheContextIsCancelled(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}
	ctx, cancel := context.WithCancel(context.Background())

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task:",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-P", "-F", paneRefFormat, "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
		runner.On("Run", mock.Anything, "", "tmux", "send-keys", "-t", "=repo_task:task", "codex", "C-m").
			Run(func(mock.Arguments) { cancel() }).
			Return(subprocess.Result{}, context.Canceled).Once(),
		runner.On("Run", liveContext, "", "tmux", "kill-session", "-t", "=repo_task").
			Return(subprocess.Result{}, nil).Once(),
	)

	_, err := repo.StartTaskSession(ctx, &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{Command: []string{"codex"}})

	require.ErrorIs(t, err, context.Canceled)
}

func TestRepositoryStartTaskSession_CleansUpSessionWhenPrefillFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task:",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-P", "-F", paneRefFormat, "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
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
		expectTmuxRunWithStdin(runner, subprocess.RunWithStdinOptions{
			Cwd:   "",
			Name:  "tmux",
			Args:  []string{"load-buffer", "-b", "rig-prefill-repo_task-task", "-"},
			Stdin: "fix billing retry flow",
		}, subprocess.Result{}, errors.New("load-buffer failed")),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"kill-session", "-t", "=repo_task",
		),
	)

	_, err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.EqualError(t, err, "load task input into tmux buffer: load-buffer failed")
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
	}, nil)

	require.NoError(t, err)
}

func TestRepositoryAttachTaskSession_AttachesWhenOutsideTmux(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.getenv = func(string) string { return "" }

	expectTmuxRun(runner, subprocess.Result{}, nil, "attach-session", "-t", "=repo_task")

	err := repo.AttachTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	}, nil)

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
	}, nil)

	require.ErrorIs(t, err, core.ErrTaskSessionNotFound)
}

// attachTestPane is an agent's pane, %7 on the server the attach tests'
// client talks to.
var attachTestPane = core.TmuxPaneRef{
	Server: core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722},
	ID:     "%7",
}

func attachTestRepository(t *testing.T, insideTmux bool) (*repository, *subprocess.MockRunner) {
	t.Helper()
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.getenv = func(key string) string {
		if key == "TMUX" && insideTmux {
			return "/private/tmp/tmux-501/default,4722,0"
		}
		return ""
	}
	return repo, runner
}

// expectClientServer expects the check of which tmux server the client talks
// to, reporting server.
func expectClientServer(runner *subprocess.MockRunner, server core.TmuxServer) {
	expectTmuxRun(runner, subprocess.Result{Stdout: fmt.Sprintf("%s\t%d\n", server.SocketPath, server.PID)}, nil,
		"display-message", "-p", "-t", "%7", "#{socket_path}\t#{pid}")
}

var outsideTmuxPaneArgs = []string{
	"select-window", "-t", "%7", ";",
	"select-pane", "-t", "%7", ";",
	"attach-session", "-t", "%7",
}

func TestRepositoryAttachTaskSession_SwitchesClientToThePaneWhenInsideTmux(t *testing.T) {
	repo, runner := attachTestRepository(t, true)
	expectClientServer(runner, attachTestPane.Server)
	// switch-client to a pane selects its session, window and pane at once.
	expectTmuxRun(runner, subprocess.Result{}, nil, "switch-client", "-t", "%7")

	pane := attachTestPane
	err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}, &pane)

	require.NoError(t, err)
}

func TestRepositoryAttachTaskSession_SelectsThePaneBeforeAttachingWhenOutsideTmux(t *testing.T) {
	repo, runner := attachTestRepository(t, false)
	expectClientServer(runner, attachTestPane.Server)
	// attach-session selects the window of a pane target but not the pane.
	expectTmuxRun(runner, subprocess.Result{}, nil, outsideTmuxPaneArgs...)

	pane := attachTestPane
	err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}, &pane)

	require.NoError(t, err)
}

func TestRepositoryAttachTaskSession_AttachesToTheSessionOnAnotherTmuxServer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		insideTmux  bool
		sessionArgs []string
	}{
		{name: "inside tmux", insideTmux: true, sessionArgs: []string{"switch-client", "-t", "=repo_task"}},
		{name: "outside tmux", sessionArgs: []string{"attach-session", "-t", "=repo_task"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, runner := attachTestRepository(t, tc.insideTmux)
			// The client talks to another server, whose %7 is another pane.
			expectClientServer(runner, core.TmuxServer{SocketPath: "/private/tmp/tmux-501/other", PID: 9001})
			expectTmuxRun(runner, subprocess.Result{}, nil, tc.sessionArgs...)

			pane := attachTestPane
			err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}, &pane)

			require.NoError(t, err)
		})
	}
}

func TestRepositoryAttachTaskSession_AttachesToTheSessionWhenTheServerCheckFails(t *testing.T) {
	repo, runner := attachTestRepository(t, true)
	expectTmuxRun(runner, subprocess.Result{Stderr: "lost server"}, errors.New("exit status 1"),
		"display-message", "-p", "-t", "%7", "#{socket_path}\t#{pid}")
	expectTmuxRun(runner, subprocess.Result{}, nil, "switch-client", "-t", "=repo_task")

	pane := attachTestPane
	err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}, &pane)

	require.NoError(t, err)
}

func TestRepositoryAttachTaskSession_FallsBackToTheSessionWhenThePaneIsGone(t *testing.T) {
	for _, tc := range []struct {
		name        string
		insideTmux  bool
		paneArgs    []string
		sessionArgs []string
	}{
		{
			name:        "inside tmux",
			insideTmux:  true,
			paneArgs:    []string{"switch-client", "-t", "%7"},
			sessionArgs: []string{"switch-client", "-t", "=repo_task"},
		},
		{
			name:        "outside tmux",
			paneArgs:    outsideTmuxPaneArgs,
			sessionArgs: []string{"attach-session", "-t", "=repo_task"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, runner := attachTestRepository(t, tc.insideTmux)
			expectClientServer(runner, attachTestPane.Server)
			missingPane := subprocess.CommandError{
				Name:   "tmux",
				Args:   tc.paneArgs,
				Stderr: "can't find pane: %7",
				Err:    errors.New("exit status 1"),
			}
			expectTmuxRun(runner, subprocess.Result{Stderr: "can't find pane: %7"}, missingPane, tc.paneArgs...)
			expectTmuxRun(runner, subprocess.Result{}, nil, tc.sessionArgs...)

			pane := attachTestPane
			err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}, &pane)

			require.NoError(t, err)
		})
	}
}

func TestRepositoryAttachTaskSession_ReportsAMissingSessionBehindAGonePane(t *testing.T) {
	repo, runner := attachTestRepository(t, true)
	expectClientServer(runner, attachTestPane.Server)
	expectTmuxRun(runner, subprocess.Result{Stderr: "can't find pane: %7"}, errors.New("exit status 1"),
		"switch-client", "-t", "%7")
	expectTmuxRun(runner, subprocess.Result{Stderr: "can't find session: repo_task"}, errors.New("exit status 1"),
		"switch-client", "-t", "=repo_task")

	pane := attachTestPane
	err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}, &pane)

	require.ErrorIs(t, err, core.ErrTaskSessionNotFound)
}

func TestRepositoryAttachTaskSession_ReturnsOtherPaneAttachErrors(t *testing.T) {
	repo, runner := attachTestRepository(t, true)
	expectClientServer(runner, attachTestPane.Server)
	expectTmuxRun(runner, subprocess.Result{Stderr: "no current client"}, errors.New("exit status 1"),
		"switch-client", "-t", "%7")

	pane := attachTestPane
	err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}, &pane)

	require.EqualError(t, err, "exit status 1")
}

func TestRepositoryInspectTaskSession_ReportsAnExistingSession(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(runner, subprocess.Result{}, nil, "has-session", "-t", "=repo_task:")

	state, err := repo.InspectTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.NoError(t, err)
	require.True(t, state.Exists)
}

// noTmuxServerStderr lists the ways tmux reports that no server is running.
var noTmuxServerStderr = []struct {
	name   string
	stderr string
}{
	{name: "no socket file", stderr: "error connecting to /tmp/tmux-1000/default (No such file or directory)"},
	{name: "stale socket file", stderr: "no server running on /tmp/tmux-1000/default"},
}

// tmuxPermissionDeniedStderr is a connection failure that is not a missing
// server.
const tmuxPermissionDeniedStderr = "error connecting to /tmp/tmux-1000/default (Permission denied)"

// expectTmuxFailure expects one tmux call that exits 1 after printing stderr.
func expectTmuxFailure(runner *subprocess.MockRunner, stderr string, args ...string) *mock.Call {
	return expectTmuxRun(runner, subprocess.Result{Stderr: stderr}, subprocess.CommandError{
		Name:   "tmux",
		Args:   args,
		Stderr: stderr,
		Err:    errors.New("exit status 1"),
	}, args...)
}

func TestRepositoryInspectTaskSession_ReportsAMissingSessionOrServerAsMissing(t *testing.T) {
	cases := append([]struct {
		name   string
		stderr string
	}{{name: "missing session", stderr: "can't find session: repo_task"}}, noTmuxServerStderr...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := subprocess.NewMockRunner(t)
			repo := New(runner).(*repository)
			expectTmuxFailure(runner, tc.stderr, "has-session", "-t", "=repo_task:")

			state, err := repo.InspectTaskSession(context.Background(), &core.Task{
				TmuxSession: "repo_task",
			})

			require.NoError(t, err)
			require.False(t, state.Exists)
		})
	}
}

func TestRepositoryInspectTaskSession_ReturnsOtherTmuxFailures(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxFailure(runner, tmuxPermissionDeniedStderr, "has-session", "-t", "=repo_task:")

	_, err := repo.InspectTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.ErrorContains(t, err, "Permission denied")
}

// A session name with a dot, as a repository directory such as my.app gives,
// is still found: the target names the session, not a window and pane.
func TestRepositoryInspectTaskSession_FindsASessionWhoseNameHasADot(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(runner, subprocess.Result{}, nil, "has-session", "-t", "=my.app_task:")

	state, err := repo.InspectTaskSession(context.Background(), &core.Task{
		TmuxSession: "my.app_task",
	})

	require.NoError(t, err)
	require.True(t, state.Exists)
}

// inventoryLine renders one paneInventoryFormat line on the test tmux server.
func inventoryLine(session, windowIndex, window, paneIndex, paneID, command, panePID string) string {
	return strings.Join([]string{
		"/private/tmp/tmux-501/default", "4722", session, windowIndex, window, paneIndex, paneID, command, panePID,
	}, "\t") + "\n"
}

func expectPaneInventory(runner *subprocess.MockRunner, result subprocess.Result, err error) *mock.Call {
	return expectTmuxRun(runner, result, err, "list-panes", "-a", "-F", paneInventoryFormat)
}

func TestRepositoryInspectTaskSessions_SixTasksUseOneTmuxAndOneProcessInventory(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return now }

	expectPaneInventory(runner, subprocess.Result{
		Stdout: inventoryLine("repo_one", "1", "task", "0", "%1", "zsh", "100") +
			inventoryLine("repo_two", "1", "task", "0", "%2", "codex", "200"),
	}, nil)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{Stdout: "  5002 100 01:30 codex\n  5013 999 00:10 vim\n"}, nil).Once()

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
		{ID: "task-2", TmuxSession: "repo_two"},
		{ID: "task-3", TmuxSession: "repo_missing"},
		{ID: "task-4", TmuxSession: "repo_missing_four"},
		{ID: "task-5", TmuxSession: "repo_missing_five"},
		{ID: "task-6", TmuxSession: "repo_missing_six"},
	})

	require.NoError(t, err)
	// Tasks whose Session is missing have no panes.
	require.Equal(t, map[string][]string{"task-1": {"%1"}, "task-2": {"%2"}}, snapshot.TaskSessionPanes)
	require.Equal(t, []core.PaneProcess{{Command: "codex", StartedAt: now.Add(-90 * time.Second)}},
		snapshot.Panes[0].Children)
}

func TestRepositoryInspectTaskSessions_ReportsEveryPaneWithServerIdentity(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return now }

	// The task's Session has a split task window and a claude in another
	// window; an unrelated session runs codex. Every pane is reported, and the
	// task's Session lists the panes of all its windows.
	expectPaneInventory(runner, subprocess.Result{
		Stdout: inventoryLine("repo_one", "1", "task", "0", "%1", "2.1.295", "100") +
			inventoryLine("repo_one", "1", "task", "1", "%7", "zsh", "101") +
			inventoryLine("repo_one", "3", "side agent", "0", "%9", "codex", "102") +
			inventoryLine("scratch", "1", "zsh", "0", "%12", "codex", "300"),
	}, nil)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{Stdout: "  5003 100 01:30 claude\n" +
			"  5004 102 00:20 codex\n" +
			"  5005 300 1-00:00:00 codex\n" +
			"  5006 999 00:10 vim\n"}, nil).Once()

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.NoError(t, err)
	require.Equal(t, core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722}, snapshot.Server)
	require.Equal(t, []core.TmuxPane{
		{
			ID:          "%1",
			Session:     "repo_one",
			WindowIndex: 1,
			WindowName:  "task",
			PaneIndex:   0,
			Command:     "2.1.295",
			PID:         100,
			Children:    []core.PaneProcess{{Command: "claude", StartedAt: now.Add(-90 * time.Second)}},
		},
		{ID: "%7", Session: "repo_one", WindowIndex: 1, WindowName: "task", PaneIndex: 1, Command: "zsh", PID: 101},
		{
			ID:          "%9",
			Session:     "repo_one",
			WindowIndex: 3,
			WindowName:  "side agent",
			PaneIndex:   0,
			Command:     "codex",
			PID:         102,
			Children:    []core.PaneProcess{{Command: "codex", StartedAt: now.Add(-20 * time.Second)}},
		},
		{
			ID:          "%12",
			Session:     "scratch",
			WindowIndex: 1,
			WindowName:  "zsh",
			PaneIndex:   0,
			Command:     "codex",
			PID:         300,
			Children:    []core.PaneProcess{{Command: "codex", StartedAt: now.Add(-24 * time.Hour)}},
		},
	}, snapshot.Panes)
	require.Equal(t, map[string][]string{"task-1": {"%1", "%7", "%9"}}, snapshot.TaskSessionPanes)
}

func TestRepositoryInspectTaskSessions_ListsPaneLinkedIntoSeveralSessionsOnce(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	// list-panes -a repeats a pane for every session it is linked into. %1 is
	// first listed under a session grouped with the task Session; %5 is in a
	// window linked into two sessions that are not task Sessions.
	expectPaneInventory(runner, subprocess.Result{
		Stdout: inventoryLine("repo_one-view", "1", "task", "0", "%1", "codex", "100") +
			inventoryLine("alpha", "2", "logs", "0", "%5", "tail", "200") +
			inventoryLine("repo_one", "1", "task", "0", "%1", "codex", "100") +
			inventoryLine("beta", "4", "logs", "0", "%5", "tail", "200"),
	}, nil)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{}, nil).Once()

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.NoError(t, err)
	require.Equal(t, []core.TmuxPane{
		{ID: "%1", Session: "repo_one", WindowIndex: 1, WindowName: "task", Command: "codex", PID: 100},
		{ID: "%5", Session: "alpha", WindowIndex: 2, WindowName: "logs", Command: "tail", PID: 200},
	}, snapshot.Panes)
	require.Equal(t, map[string][]string{"task-1": {"%1"}}, snapshot.TaskSessionPanes)
}

func TestRepositoryInspectTaskSessions_AcceptsWindowsWithEmptyNames(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	// tmux allows rename-window '', and the inventory covers every window on
	// the server, so an unnamed window must not hide the task Sessions.
	expectPaneInventory(runner, subprocess.Result{
		Stdout: inventoryLine("scratch", "1", "", "0", "%3", "zsh", "300") +
			inventoryLine("scratch", "2", "   ", "0", "%4", "zsh", "301") +
			inventoryLine("repo_one", "1", "task", "0", "%1", "codex", "100"),
	}, nil)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{}, nil).Once()

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.NoError(t, err)
	require.Equal(t, []core.TmuxPane{
		{ID: "%3", Session: "scratch", WindowIndex: 1, Command: "zsh", PID: 300},
		{ID: "%4", Session: "scratch", WindowIndex: 2, Command: "zsh", PID: 301},
		{ID: "%1", Session: "repo_one", WindowIndex: 1, WindowName: "task", Command: "codex", PID: 100},
	}, snapshot.Panes)
	require.Equal(t, map[string][]string{"task-1": {"%1"}}, snapshot.TaskSessionPanes)
}

func TestRepositoryInspectTaskSessions_LeavesServerUnknownWithoutSocketPath(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	// tmux before 3.2 has no socket_path format and expands it to nothing.
	expectPaneInventory(runner, subprocess.Result{
		Stdout: "\t4722\trepo_one\t1\ttask\t0\t%1\tcodex\t100\n",
	}, nil)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{}, nil).Once()

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.NoError(t, err)
	require.True(t, snapshot.Server.IsZero())
	require.Len(t, snapshot.Panes, 1)
	require.Equal(t, "%1", snapshot.Panes[0].ID)
	require.Equal(t, map[string][]string{"task-1": {"%1"}}, snapshot.TaskSessionPanes)
}

func TestRepositoryInspectTaskSessions_NoServerReportsEveryTaskMissing(t *testing.T) {
	for _, noServer := range noTmuxServerStderr {
		t.Run(noServer.name, func(t *testing.T) {
			runner := subprocess.NewMockRunner(t)
			repo := New(runner).(*repository)
			expectTmuxFailure(runner, noServer.stderr, "list-panes", "-a", "-F", paneInventoryFormat)

			snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
				{ID: "task-1", TmuxSession: "repo_one"},
			})

			require.NoError(t, err)
			require.Zero(t, snapshot)
		})
	}
}

func TestRepositoryInspectTaskSessions_TmuxConnectionFailureMakesSnapshotUnknown(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	expectTmuxFailure(runner, tmuxPermissionDeniedStderr, "list-panes", "-a", "-F", paneInventoryFormat)

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.ErrorContains(t, err, "Permission denied")
	require.Zero(t, snapshot)
}

// processLine renders one processInventoryColumns line.
func processLine(pid, ppid, elapsed, command string) string {
	return "  " + pid + " " + ppid + " " + elapsed + " " + command + "\n"
}

// expectLocatorInventory expects the pane inventory of a task Session with a
// task window and a side window, plus an unrelated session, and returns the
// task those panes belong to.
func expectLocatorInventory(runner *subprocess.MockRunner) *core.Task {
	expectPaneInventory(runner, subprocess.Result{
		Stdout: inventoryLine("repo_one", "1", "task", "0", "%1", "zsh", "100") +
			inventoryLine("repo_one", "3", "agents", "0", "%9", "codex", "102") +
			inventoryLine("scratch", "1", "zsh", "0", "%12", "zsh", "300"),
	}, nil)
	return &core.Task{ID: "task-1", TmuxSession: "repo_one"}
}

func TestRepositoryLocateProcessPane_FindsPaneWhoseRootProcessIsAnAncestor(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	task := expectLocatorInventory(runner)
	// The hook forwarder (4000) runs under a shell started by the agent CLI,
	// which the side window's pane shell (102) started.
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{Stdout: processLine("102", "1", "05:00", "zsh") +
			processLine("3000", "102", "04:00", "codex") +
			processLine("3999", "3000", "00:01", "sh") +
			processLine("4000", "3999", "00:01", "sh") +
			processLine("300", "1", "09:00", "zsh")}, nil).Once()

	snapshot, location, err := repo.LocateProcessPane(context.Background(), task, 4000)

	require.NoError(t, err)
	require.Equal(t, core.ProcessPane{Pane: "%9", Chain: []string{"sh", "sh", "codex", "zsh"}}, location)
	require.Equal(t, core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722}, snapshot.Server)
	require.Equal(t, map[string][]string{"task-1": {"%1", "%9"}}, snapshot.TaskSessionPanes)
	require.Len(t, snapshot.Panes, 3)
}

func TestRepositoryLocateProcessPane_ReturnsNoPaneWhenNoPaneIsAnAncestor(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	task := expectLocatorInventory(runner)
	// A Codex using the shared daemon runs hooks under the daemon, which
	// launchd started, not under any pane.
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{Stdout: processLine("1", "0", "9-00:00:00", "launchd") +
			processLine("1510", "1", "1-00:00:00", "codex") +
			processLine("23340", "1510", "10:00:00", "codex") +
			processLine("4000", "23340", "00:01", "sh") +
			processLine("102", "1", "05:00", "zsh")}, nil).Once()

	_, location, err := repo.LocateProcessPane(context.Background(), task, 4000)

	require.NoError(t, err)
	require.Equal(t, core.ProcessPane{}, location)
}

func TestRepositoryLocateProcessPane_StopsAfterTheAncestryLimit(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	task := expectLocatorInventory(runner)
	// The chain does reach pane %9's shell (102), but only beyond the limit.
	var processes strings.Builder
	parent := 102
	for pid := 5000; pid < 5000+maxProcessAncestry; pid++ {
		processes.WriteString(processLine(strconv.Itoa(pid), strconv.Itoa(parent), "00:01", "sh"))
		parent = pid
	}
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{Stdout: processes.String()}, nil).Once()

	_, location, err := repo.LocateProcessPane(context.Background(), task, parent)

	require.NoError(t, err)
	require.Equal(t, core.ProcessPane{}, location)
}

func TestRepositoryLocateProcessPane_FailsWithoutProcessInventory(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	task := expectLocatorInventory(runner)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{}, errors.New("ps unavailable")).Once()

	_, location, err := repo.LocateProcessPane(context.Background(), task, 4000)

	require.ErrorContains(t, err, "process inventory unavailable")
	require.Equal(t, core.ProcessPane{}, location)
}

func TestRepositoryLocateProcessPane_WithoutPIDOnlyInspects(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	task := expectLocatorInventory(runner)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{Stdout: processLine("3000", "102", "04:00", "codex")}, nil).Once()

	snapshot, location, err := repo.LocateProcessPane(context.Background(), task, 0)

	require.NoError(t, err)
	require.Equal(t, core.ProcessPane{}, location)
	// Without a PID nothing is walked, but process evidence still describes
	// each pane.
	require.Equal(t, "%9", snapshot.Panes[1].ID)
	require.Len(t, snapshot.Panes[1].Children, 1)
	require.Equal(t, "codex", snapshot.Panes[1].Children[0].Command)
}

func TestRepositoryLocateProcessPane_NoServerFindsNoPane(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	expectPaneInventory(runner, subprocess.Result{}, subprocess.CommandError{
		Name:   "tmux",
		Args:   []string{"list-panes", "-a", "-F", paneInventoryFormat},
		Stderr: "no server running on /private/tmp/tmux-501/default",
		Err:    errors.New("exit status 1"),
	})

	snapshot, location, err := repo.LocateProcessPane(context.Background(), &core.Task{
		ID:          "task-1",
		TmuxSession: "repo_one",
	}, 4000)

	require.NoError(t, err)
	require.Equal(t, core.ProcessPane{}, location)
	require.Empty(t, snapshot.Panes)
}

func TestRepositoryInspectTaskSessions_ReportsPaneChildProcessStartTimes(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return now }

	expectPaneInventory(runner, subprocess.Result{
		Stdout: inventoryLine("repo_one", "1", "task", "0", "%1", "zsh", "100"),
	}, nil)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{Stdout: "  5007 100 1-02:03:04 codex\n" +
			"  5008 100 00:05 codex\n" +
			"  5009 100 12:00 ChatGPT Helper\n" +
			"  5010 100 not-elapsed tail\n"}, nil).Once()

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.NoError(t, err)
	require.Equal(t, []core.PaneProcess{
		{Command: "codex", StartedAt: now.Add(-(26*time.Hour + 3*time.Minute + 4*time.Second))},
		{Command: "codex", StartedAt: now.Add(-5 * time.Second)},
		{Command: "ChatGPT Helper", StartedAt: now.Add(-12 * time.Minute)},
		{Command: "tail"},
	}, snapshot.Panes[0].Children)
}

func TestRepositoryInspectTaskSessions_PreservesDirectCommandsWhenProcessInventoryFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectPaneInventory(runner, subprocess.Result{
		Stdout: inventoryLine("repo_one", "1", "task", "0", "%1", "codex", "100") +
			inventoryLine("repo_two", "1", "task", "0", "%2", "zsh", "200"),
	}, nil)
	runner.On("Run", mock.Anything, "", "ps", "-axo", processInventoryColumns).
		Return(subprocess.Result{}, errors.New("ps unavailable")).Once()

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
		{ID: "task-2", TmuxSession: "repo_two"},
	})

	require.NoError(t, err)
	require.Len(t, snapshot.Panes, 2)
	require.Equal(t, "codex", snapshot.Panes[0].Command)
	require.Equal(t, "zsh", snapshot.Panes[1].Command)
	for _, pane := range snapshot.Panes {
		require.True(t, pane.ChildProcessEvidenceUnavailable, pane.ID)
		require.Empty(t, pane.Children, pane.ID)
	}
}

func TestRepositoryInspectTaskSessions_UnexpectedTmuxFailureMakesSnapshotUnknown(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectPaneInventory(runner, subprocess.Result{}, errors.New("tmux protocol failure"))

	snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.Error(t, err)
	require.Zero(t, snapshot)
}

func TestRepositoryInspectTaskSessions_MalformedTmuxOutputMakesSnapshotUnknown(t *testing.T) {
	cases := map[string]string{
		"missing field":      "/private/tmp/tmux-501/default\t4722\trepo_one\t1\ttask\t0\t%1\tzsh\n",
		"missing pane ID":    inventoryLine("repo_one", "1", "task", "0", "", "zsh", "100"),
		"non-numeric window": inventoryLine("repo_one", "one", "task", "0", "%1", "zsh", "100"),
		"non-numeric pane":   inventoryLine("repo_one", "1", "task", "zero", "%1", "zsh", "100"),
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) {
			runner := subprocess.NewMockRunner(t)
			repo := New(runner).(*repository)
			expectPaneInventory(runner, subprocess.Result{Stdout: output}, nil)

			snapshot, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
				{ID: "task-1", TmuxSession: "repo_one"},
			})

			require.EqualError(t, err, "parse tmux pane inventory: incomplete output")
			require.Zero(t, snapshot)
		})
	}
}

func TestRepositoryStartTaskSession_RefusesASessionThatAlreadyExists(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	// Nothing is typed: an agent may be running in the session.
	expectTmuxRun(runner, subprocess.Result{}, nil, "has-session", "-t", "=repo_task:")

	pane, err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:     []string{"claude"},
		ReadyMarker: "❯",
	})

	require.ErrorIs(t, err, core.ErrTaskSessionExists)
	require.EqualError(t, err, "task session already exists: repo_task")
	require.Zero(t, pane)
}

// expectAgentPane expects the split-window that opens an agent pane beside
// pane %50 of the repo_task session, reporting result. The new pane spans the
// window's height at its right edge, so agents split in one after another sit
// left to right in that order.
func expectAgentPane(runner *subprocess.MockRunner, result subprocess.Result, err error) *mock.Call {
	return expectTmuxRun(runner, result, err,
		"split-window", "-d", "-h", "-f", "-P", "-F", paneRefFormat, "-t", "%50", "-c", "/tmp/repo-task",
	)
}

// liveContext matches a context that is not done, as cleanup after a failed
// launch must get even when the caller's context was cancelled.
var liveContext = mock.MatchedBy(func(ctx context.Context) bool { return ctx.Err() == nil })

var agentPaneTask = &core.Task{TmuxSession: "repo_task", WorktreePath: "/tmp/repo-task"}

func TestRepositorySplitAgentPane_LaunchesTheAgentInANewPaneBesideTheOthers(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectAgentPane(runner, subprocess.Result{Stdout: "%12\t/private/tmp/tmux-501/default\t4722\n"}, nil),
		expectTmuxRun(runner, subprocess.Result{}, nil, "select-layout", "-t", "%12", "even-horizontal"),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "%12", "claude --settings '{\"a\": 1}'", "C-m",
		),
	)

	pane, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command:      []string{"claude", "--settings", `{"a": 1}`},
		ReadyMarker:  "❯",
		PrefillInput: []string{"never typed"},
	})

	require.NoError(t, err)
	require.Equal(t, core.TmuxPaneRef{
		Server: core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722},
		ID:     "%12",
	}, pane)
}

func TestRepositorySplitAgentPane_SplitsTheTaskWindowWithoutAPaneToSplitBeside(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectTaskWindowPane(runner, subprocess.Result{Stdout: "%12\t/private/tmp/tmux-501/default\t4722\n"}, nil),
		expectTmuxRun(runner, subprocess.Result{}, nil, "select-layout", "-t", "%12", "even-horizontal"),
		expectTmuxRun(runner, subprocess.Result{}, nil, "send-keys", "-t", "%12", "codex", "C-m"),
	)

	pane, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "", core.TaskSessionLaunchSpec{
		Command: []string{"codex"},
	})

	require.NoError(t, err)
	require.Equal(t, "%12", pane.ID)
}

// The panes stay unevenly wide, but the agent still starts.
func TestRepositorySplitAgentPane_LaunchesTheAgentWhenThePanesCannotBeEvenedOut(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectAgentPane(runner, subprocess.Result{Stdout: "%12\t/private/tmp/tmux-501/default\t4722\n"}, nil),
		expectTmuxRun(runner, subprocess.Result{}, errors.New("select-layout failed"),
			"select-layout", "-t", "%12", "even-horizontal",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil, "send-keys", "-t", "%12", "claude", "C-m"),
	)

	pane, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command: []string{"claude"},
	})

	require.NoError(t, err)
	require.Equal(t, "%12", pane.ID)
}

func TestRepositorySplitAgentPane_ClosesThePaneWhenTheLaunchFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	ctx, cancel := context.WithCancel(context.Background())

	mock.InOrder(
		expectAgentPane(runner, subprocess.Result{Stdout: "%12\t/private/tmp/tmux-501/default\t4722\n"}, nil),
		expectTmuxRun(runner, subprocess.Result{}, nil, "select-layout", "-t", "%12", "even-horizontal"),
		runner.On("Run", mock.Anything, "", "tmux", "send-keys", "-t", "%12", "claude", "C-m").
			Run(func(mock.Arguments) { cancel() }).
			Return(subprocess.Result{}, context.Canceled).Once(),
		// Only the new pane closes, and the panes left share the window again.
		runner.On("Run", liveContext, "", "tmux", "kill-pane", "-t", "%12").
			Return(subprocess.Result{}, nil).Once(),
		runner.On("Run", liveContext, "", "tmux", "select-layout", "-t", "%50", "even-horizontal").
			Return(subprocess.Result{}, nil).Once(),
	)

	pane, err := repo.SplitAgentPane(ctx, agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command: []string{"claude"},
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, pane)
}

// tmuxFailure is the error of a failed tmux command whose stderr says why.
func tmuxFailure(stderr string) error {
	return subprocess.CommandError{Name: "tmux", Stderr: stderr, Err: errors.New("exit status 1")}
}

// expectTaskWindowPane expects the split-window that opens an agent pane at the
// right edge of the repo_task session's task window, reporting result.
func expectTaskWindowPane(runner *subprocess.MockRunner, result subprocess.Result, err error) *mock.Call {
	return expectTmuxRun(runner, result, err,
		"split-window", "-d", "-h", "-f", "-P", "-F", paneRefFormat,
		"-t", "=repo_task:task", "-c", "/tmp/repo-task",
	)
}

// The pane to split beside closed after the Session was recreated. The task
// window's right edge is where the agent would have gone anyway.
func TestRepositorySplitAgentPane_SplitsTheTaskWindowWhenThePaneToSplitBesideIsGone(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectAgentPane(runner, subprocess.Result{}, tmuxFailure("can't find pane: %50")),
		expectTaskWindowPane(runner, subprocess.Result{Stdout: "%12\t/private/tmp/tmux-501/default\t4722\n"}, nil),
		expectTmuxRun(runner, subprocess.Result{}, nil, "select-layout", "-t", "%12", "even-horizontal"),
		expectTmuxRun(runner, subprocess.Result{}, nil, "send-keys", "-t", "%12", "claude", "C-m"),
	)

	pane, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command: []string{"claude"},
	})

	require.NoError(t, err)
	require.Equal(t, "%12", pane.ID)
}

func TestRepositorySplitAgentPane_ReportsAMissingSession(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectAgentPane(runner, subprocess.Result{}, tmuxFailure("can't find pane: %50")),
		expectTaskWindowPane(runner, subprocess.Result{}, tmuxFailure("can't find session: repo_task")),
	)

	_, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command: []string{"claude"},
	})

	require.ErrorIs(t, err, core.ErrTaskSessionNotFound)
}

// The Session is there, but its task window was closed: the agent has nowhere
// to go, and the Session is not reported missing.
func TestRepositorySplitAgentPane_ReportsAGoneTaskWindow(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectAgentPane(runner, subprocess.Result{}, tmuxFailure("can't find pane: %50")),
		expectTaskWindowPane(runner, subprocess.Result{}, tmuxFailure("can't find window: task")),
	)

	_, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command: []string{"claude"},
	})

	require.ErrorContains(t, err, `task window "task" is gone`)
	require.NotErrorIs(t, err, core.ErrTaskSessionNotFound)
}

func TestRepositorySplitAgentPane_ReportsAPaneItCouldNotClose(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	launchErr := errors.New("send-keys failed")
	closeErr := errors.New("kill-pane failed")

	mock.InOrder(
		expectAgentPane(runner, subprocess.Result{Stdout: "%12\t/private/tmp/tmux-501/default\t4722\n"}, nil),
		expectTmuxRun(runner, subprocess.Result{}, nil, "select-layout", "-t", "%12", "even-horizontal"),
		expectTmuxRun(runner, subprocess.Result{}, launchErr, "send-keys", "-t", "%12", "claude", "C-m"),
		// The pane stays, so there is nothing to even out again.
		expectTmuxRun(runner, subprocess.Result{}, closeErr, "kill-pane", "-t", "%12"),
	)

	pane, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command: []string{"claude"},
	})

	require.ErrorIs(t, err, launchErr)
	require.ErrorIs(t, err, closeErr)
	require.Zero(t, pane)
}

func TestRepositorySplitAgentPane_FailsWhenTmuxReportsNoPane(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectAgentPane(runner, subprocess.Result{Stdout: "\n"}, nil)

	_, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command: []string{"claude"},
	})

	require.EqualError(t, err, `tmux reported no usable pane for the new agent pane: "\n"`)
}

// Output that names a pane but is not paneRefFormat cannot place the agent,
// so its pane is closed rather than left as a stray shell.
func TestRepositorySplitAgentPane_ClosesAPaneWhoseReportedPaneDoesNotParse(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectAgentPane(runner, subprocess.Result{Stdout: "%12\n"}, nil),
		expectTmuxRun(runner, subprocess.Result{}, nil, "kill-pane", "-t", "%12"),
		expectTmuxRun(runner, subprocess.Result{}, nil, "select-layout", "-t", "%50", "even-horizontal"),
	)

	pane, err := repo.SplitAgentPane(context.Background(), agentPaneTask, "%50", core.TaskSessionLaunchSpec{
		Command: []string{"claude"},
	})

	require.EqualError(t, err, `tmux reported no usable pane for the new agent pane: "%12\n"`)
	require.Zero(t, pane)
}

func TestParsePaneRef(t *testing.T) {
	server := core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722}
	cases := []struct {
		name   string
		output string
		pane   core.TmuxPaneRef
		ok     bool
	}{
		{name: "pane and server", output: "%3\t/private/tmp/tmux-501/default\t4722\n",
			pane: core.TmuxPaneRef{Server: server, ID: "%3"}, ok: true},
		{name: "tmux without socket_path leaves the server unknown", output: "%3\t\t4722\n",
			pane: core.TmuxPaneRef{ID: "%3"}, ok: true},
		{name: "missing fields keep the pane ID to close", output: "%3\n", pane: core.TmuxPaneRef{ID: "%3"}},
		{name: "no pane ID", output: "repo_task:\n"},
		{name: "nothing", output: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pane, ok := parsePaneRef(tc.output)

			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.pane, pane)
		})
	}
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

func expectTmuxRunWithStdin(
	runner *subprocess.MockRunner,
	opts subprocess.RunWithStdinOptions,
	result subprocess.Result,
	err error,
) *mock.Call {
	return runner.On("RunWithStdin", mock.Anything, opts).Return(result, err).Once()
}
