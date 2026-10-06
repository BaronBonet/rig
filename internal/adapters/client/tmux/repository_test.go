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

func TestRepositoryStartTaskSession_LaunchesTheCommandWithoutTypingThePrompt(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	var slept []time.Duration
	repo.sleep = func(d time.Duration) { slept = append(slept, d) }

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:task", "codex", "C-m",
		),
	)

	// No capture-pane or paste-buffer expectation: the prompt is typed by
	// PrefillTaskSession, once the provider is known to be ready.
	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.NoError(t, err)
	require.Equal(t, []time.Duration{promptSubmitDelay}, slept)
}

func TestRepositoryPrefillTaskSession_TypesThePromptOnTheProvidersPromptNotTheShellsEcho(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	var slept []time.Duration
	repo.sleep = func(d time.Duration) { slept = append(slept, d) }

	// A shell prompt that uses the provider's marker shows it next to the
	// launch command before the provider has started.
	shellEcho := "~/repo-task via v2\n❯ claude --model opus\n"
	providerPrompt := shellEcho + "\n Claude Code v2.1\n\n❯ Try \"how do I log an error?\"\n"
	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{Stdout: shellEcho}, nil,
			"capture-pane", "-t", "=repo_task:task", "-p",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: providerPrompt}, nil,
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

	err := repo.PrefillTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"claude", "--model", "opus"},
		ReadyMarker:  "❯",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.NoError(t, err)
	require.Equal(t, []time.Duration{500 * time.Millisecond, promptInputSettleDelay}, slept,
		"one poll while only the shell's echo is up, then the settle delay")
}

func TestRepositoryPrefillTaskSession_PrefillsLargeInputThroughTmuxBuffer(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	repo.sleep = func(time.Duration) {}

	prompt := strings.Repeat("debug output\n", 5000)

	mock.InOrder(
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

	err := repo.PrefillTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{prompt},
	})

	require.NoError(t, err)
}

func TestRepositoryPrefillTaskSession_TimesOutWhileOnlyTheShellEchoIsUp(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	clock := time.Unix(0, 0)
	repo.now = func() time.Time { return clock }
	repo.sleep = func(d time.Duration) { clock = clock.Add(31 * time.Second) }

	expectTmuxRun(runner, subprocess.Result{Stdout: "❯ claude\n"}, nil,
		"capture-pane", "-t", "=repo_task:task", "-p",
	)

	err := repo.PrefillTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"claude"},
		ReadyMarker:  "❯",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.EqualError(t, err, "timed out waiting for ❯ prompt")
}

func TestPromptReady_SkipsTheShellsEchoOfTheLaunchCommand(t *testing.T) {
	command := shellCommandText([]string{"env", "CLAUDE_CONFIG_DIR=/home/me/.claude-work", "claude", "--model", "opus"})

	require.False(t, promptReady("❯ env CLAUDE_CONFIG_DIR=/home/me/.claude-work claude --model opus\n", "❯", command),
		"the shell's echo of the command")
	require.False(
		t,
		promptReady("❯ env CLAUDE_CONFIG_DIR=/home/me/.claude-work claude --model opus   ✔ 14:22\n", "❯", command),
		"a right prompt after the echo",
	)
	require.False(t, promptReady("❯ env CLAUDE_CONFIG_DIR=/home/me/.cla\nude-work claude --model opus\n", "❯", command),
		"an echo wrapped onto a second line")
	require.False(t, promptReady("no marker here\n", "❯", command))
	require.True(t, promptReady("❯ \n", "❯", command), "an empty input box")
	require.True(t, promptReady("❯ Try \"how do I log an error?\"\n", "❯", command), "a placeholder in the input box")
	require.True(t, promptReady("❯ claude\n\n❯ \n", "❯", command), "the input box below the echo")
	require.True(t, promptReady("anything", "", command), "no marker to wait for")
}

func TestRepositoryStartTaskSession_LeavesShellIdleWhenLaunchCommandIsEmpty(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task",
		),
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

func TestRepositoryStartTaskSession_ExportsTaskIDToTheSessionEnvironment(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
			"-e", "RIG_TASK_ID=task-123",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		ID:           "task-123",
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{})

	require.NoError(t, err)
}

func TestRepositoryStartTaskSession_ExportsTheTaskProviderConfiguration(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo_task",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-session", "-d", "-s", "repo_task", "-n", "task", "-c", "/tmp/repo-task",
			"-e", "RIG_TASK_ID=task-123",
			"-e", "CLAUDE_CONFIG_DIR=/home/me/.claude-work",
			"-e", "CODEX_HOME=/home/me/.codex-work",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "editor", "-c", "/tmp/repo-task",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		ID:           "task-123",
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
		ProviderEnv: core.ProviderEnv{
			"CODEX_HOME":        "/home/me/.codex-work",
			"CLAUDE_CONFIG_DIR": "/home/me/.claude-work",
			"GEMINI_HOME":       "",
		},
	}, core.TaskSessionLaunchSpec{})

	require.NoError(t, err)
}

func TestRepositoryStartTaskSession_CleansUpSessionWhenEditorWindowCreationFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, errors.New("no session"),
			"has-session", "-t", "=repo-billing-retry-flow",
		),
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

func TestRepositoryPrefillTaskSession_LeavesTheSessionRunningWhenTypingFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.now = func() time.Time { return time.Unix(0, 0) }
	repo.sleep = func(time.Duration) {}

	// No kill-session expectation: the provider is up, only the prompt is
	// missing, and it stays on the task record.
	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{Stdout: "›"}, nil,
			"capture-pane", "-t", "=repo_task:task", "-p",
		),
		expectTmuxRunWithStdin(runner, subprocess.RunWithStdinOptions{
			Cwd:   "",
			Name:  "tmux",
			Args:  []string{"load-buffer", "-b", "rig-prefill-repo_task-task", "-"},
			Stdin: "fix billing retry flow",
		}, subprocess.Result{}, errors.New("load-buffer failed")),
	)

	err := repo.PrefillTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"fix billing retry flow"},
	})

	require.EqualError(t, err, "load task input into tmux buffer: load-buffer failed")
}

// insideTmux runs rig in pane %3 of a tmux client, with env overriding the
// rest of its environment.
func insideTmux(repo *repository, env map[string]string) {
	repo.getenv = func(key string) string {
		switch key {
		case "TMUX":
			return "/tmp/tmux-1000/default,123,0"
		case "TMUX_PANE":
			return "%3"
		}
		return env[key]
	}
}

func TestRepositoryAttachTaskSession_SwitchesClientAndBindsAKeyBackToRig(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	insideTmux(repo, nil)

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil, "switch-client", "-t", "=repo_task"),
		expectTmuxRun(runner, subprocess.Result{Stdout: "project\n"}, nil,
			"display-message", "-p", "-t", "%3", "#{session_name}"),
		expectTmuxRun(runner, subprocess.Result{}, nil, "set-option", "-t", "=repo_task:", "@rig_session", "project"),
		expectTmuxRun(runner, subprocess.Result{}, nil, "set-option", "-g", "@rig_session", "project"),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"bind-key", "-T", "prefix", "b", "run-shell", "-C", "switch-client -t '=#{@rig_session}'"),
	)

	err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"})

	require.NoError(t, err)
}

func TestRepositoryAttachTaskSession_TheReturnKeyCanBeChangedOrTurnedOff(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	insideTmux(repo, map[string]string{"RIG_TMUX_RETURN_KEY": "e"})
	expectTmuxRun(runner, subprocess.Result{}, nil, "switch-client", "-t", "=repo_task")
	expectTmuxRun(runner, subprocess.Result{Stdout: "project"}, nil,
		"display-message", "-p", "-t", "%3", "#{session_name}")
	expectTmuxRun(runner, subprocess.Result{}, nil, "set-option", "-t", "=repo_task:", "@rig_session", "project")
	expectTmuxRun(runner, subprocess.Result{}, nil, "set-option", "-g", "@rig_session", "project")
	expectTmuxRun(runner, subprocess.Result{}, nil,
		"bind-key", "-T", "prefix", "e", "run-shell", "-C", "switch-client -t '=#{@rig_session}'")
	require.NoError(t, repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}))

	off := subprocess.NewMockRunner(t)
	repo = New(off).(*repository)
	insideTmux(repo, map[string]string{"RIG_TMUX_RETURN_KEY": "none"})
	expectTmuxRun(off, subprocess.Result{}, nil, "switch-client", "-t", "=repo_task")
	require.NoError(t, repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}))
}

func TestRepositoryAttachTaskSession_BindsNothingWhenRigCannotNameItsSession(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	insideTmux(repo, nil)
	expectTmuxRun(runner, subprocess.Result{}, nil, "switch-client", "-t", "=repo_task")
	expectTmuxRun(runner, subprocess.Result{}, errors.New("no server"),
		"display-message", "-p", "-t", "%3", "#{session_name}")

	require.NoError(t, repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"}))
}

func TestRepositoryAttachTaskSession_AFailedSwitchBindsNothing(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	insideTmux(repo, nil)
	expectTmuxRun(runner, subprocess.Result{Stderr: "no current client"}, errors.New("exit status 1"),
		"switch-client", "-t", "=repo_task")

	err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"})

	require.ErrorContains(t, err, "exit status 1")
}

func TestRepositoryAttachTaskSession_MissingSessionBindsNothing(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	insideTmux(repo, nil)
	expectTmuxRun(runner, subprocess.Result{Stderr: "can't find session: =repo_task"}, errors.New("exit status 1"),
		"switch-client", "-t", "=repo_task")

	err := repo.AttachTaskSession(context.Background(), &core.Task{TmuxSession: "repo_task"})

	require.ErrorIs(t, err, core.ErrTaskSessionNotFound)
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
		subprocess.Result{Stdout: "zsh\t100\ncodex\t200\n"},
		nil,
		"list-panes",
		"-t",
		"=repo_task:task",
		"-F",
		"#{pane_current_command}\t#{pane_pid}",
	)
	runner.On("Run", mock.Anything, "", "ps", "-axo", "ppid=,comm=").
		Return(subprocess.Result{Stdout: "  100 codex\n  999 vim\n"}, nil).Once()

	state, err := repo.InspectTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.NoError(t, err)
	require.True(t, state.Exists)
	require.Equal(t, []string{"zsh", "codex", "codex"}, state.ActiveCommands)
}

func TestRepositoryInspectTaskSessions_SixTasksUseOneTmuxAndOneProcessInventory(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(
		runner,
		subprocess.Result{Stdout: "repo_one\ttask\tzsh\t100\nrepo_two\ttask\tcodex\t200\n"},
		nil,
		"list-panes",
		"-a",
		"-F",
		"#{session_name}\t#{window_name}\t#{pane_current_command}\t#{pane_pid}",
	)
	runner.On("Run", mock.Anything, "", "ps", "-axo", "ppid=,comm=").
		Return(subprocess.Result{Stdout: "  100 codex\n  999 vim\n"}, nil).Once()

	states, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
		{ID: "task-2", TmuxSession: "repo_two"},
		{ID: "task-3", TmuxSession: "repo_missing"},
		{ID: "task-4", TmuxSession: "repo_missing_four"},
		{ID: "task-5", TmuxSession: "repo_missing_five"},
		{ID: "task-6", TmuxSession: "repo_missing_six"},
	})

	require.NoError(t, err)
	require.Equal(t, map[string]core.TaskSessionRuntimeState{
		"task-1": {Exists: true, ActiveCommands: []string{"zsh", "codex"}},
		"task-2": {Exists: true, ActiveCommands: []string{"codex"}},
		"task-3": {},
		"task-4": {},
		"task-5": {},
		"task-6": {},
	}, states)
}

func TestRepositoryInspectTaskSessions_PreservesDirectCommandsWhenProcessInventoryFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(
		runner,
		subprocess.Result{Stdout: "repo_one\ttask\tcodex\t100\nrepo_two\ttask\tzsh\t200\n"},
		nil,
		"list-panes",
		"-a",
		"-F",
		"#{session_name}\t#{window_name}\t#{pane_current_command}\t#{pane_pid}",
	)
	runner.On("Run", mock.Anything, "", "ps", "-axo", "ppid=,comm=").
		Return(subprocess.Result{}, errors.New("ps unavailable")).Once()

	states, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
		{ID: "task-2", TmuxSession: "repo_two"},
	})

	require.NoError(t, err)
	require.Equal(t, core.TaskSessionRuntimeState{
		Exists:                          true,
		ActiveCommands:                  []string{"codex"},
		ChildProcessEvidenceUnavailable: true,
	}, states["task-1"])
	require.Equal(t, core.TaskSessionRuntimeState{
		Exists:                          true,
		ActiveCommands:                  []string{"zsh"},
		ChildProcessEvidenceUnavailable: true,
	}, states["task-2"])
}

func TestRepositoryInspectTaskSessions_UnexpectedTmuxFailureMakesSnapshotUnknown(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(
		runner,
		subprocess.Result{},
		errors.New("tmux protocol failure"),
		"list-panes",
		"-a",
		"-F",
		"#{session_name}\t#{window_name}\t#{pane_current_command}\t#{pane_pid}",
	)

	states, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.Error(t, err)
	require.Nil(t, states)
}

func TestRepositoryInspectTaskSessions_MalformedTmuxOutputMakesSnapshotUnknown(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(
		runner,
		subprocess.Result{Stdout: "repo_one\ttask\tzsh\n"},
		nil,
		"list-panes",
		"-a",
		"-F",
		"#{session_name}\t#{window_name}\t#{pane_current_command}\t#{pane_pid}",
	)

	states, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_one"},
	})

	require.EqualError(t, err, "parse tmux pane inventory: incomplete output")
	require.Nil(t, states)
}

func TestRepositoryInspectTaskSession_ReportsPaneChildCommandsWhenProviderRewritesItsTitle(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	// Claude Code sets its process title to its version string, so the pane
	// command alone cannot identify the provider; the pane child comm can.
	expectTmuxRun(
		runner,
		subprocess.Result{Stdout: "2.1.200\t100\n"},
		nil,
		"list-panes",
		"-t",
		"=repo_task:task",
		"-F",
		"#{pane_current_command}\t#{pane_pid}",
	)
	runner.On("Run", mock.Anything, "", "ps", "-axo", "ppid=,comm=").
		Return(subprocess.Result{Stdout: "  100 claude\n  100 tail\n  999 vim\n"}, nil).Once()

	state, err := repo.InspectTaskSession(context.Background(), &core.Task{
		TmuxSession: "repo_task",
	})

	require.NoError(t, err)
	require.True(t, state.Exists)
	require.Equal(t, []string{"2.1.200", "claude", "tail"}, state.ActiveCommands)
}

func TestRepositoryStartTaskSession_LaunchesIntoExistingSessionWithoutRecreatingIt(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"has-session", "-t", "=repo_task",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "task\neditor\n"}, nil,
			"list-windows", "-t", "=repo_task", "-F", "#{window_name}",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:task", "claude", "C-m",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{
		Command:     []string{"claude"},
		ReadyMarker: "❯",
	})

	require.NoError(t, err)
}

func TestRepositoryStartTaskSession_RecreatesAMissingTaskWindowInAnExistingSession(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}

	// A child task's window opened the session first; the parent's own
	// window has to be added before its provider can start there.
	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"has-session", "-t", "=repo_task",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "s2\neditor\n"}, nil,
			"list-windows", "-t", "=repo_task", "-F", "#{window_name}",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "task", "-c", "/tmp/repo-task", "-e", "RIG_TASK_ID=task-1",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:task", "claude", "C-m",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		ID:           "task-1",
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{Command: []string{"claude"}, ReadyMarker: "❯"})

	require.NoError(t, err)
}

func TestRepositoryInspectTaskSession_ReturnsMissingWhenTaskWindowIsGone(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(
		runner,
		subprocess.Result{Stderr: "can't find window: task"},
		subprocess.CommandError{
			Name:   "tmux",
			Args:   []string{"list-panes", "-t", "=repo_task:task", "-F", "#{pane_current_command}\t#{pane_pid}"},
			Stderr: "can't find window: task",
			Err:    errors.New("exit status 1"),
		},
		"list-panes",
		"-t",
		"=repo_task:task",
		"-F",
		"#{pane_current_command}\t#{pane_pid}",
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

func expectTmuxRunWithStdin(
	runner *subprocess.MockRunner,
	opts subprocess.RunWithStdinOptions,
	result subprocess.Result,
	err error,
) *mock.Call {
	return runner.On("RunWithStdin", mock.Anything, opts).Return(result, err).Once()
}

func TestRepositoryStartTaskSession_AddsAChildTasksWindowToItsParentsSession(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"has-session", "-t", "=repo_task",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "task\neditor\n"}, nil,
			"list-windows", "-t", "=repo_task", "-F", "#{window_name}",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"new-window", "-d", "-t", "=repo_task", "-n", "s2", "-c", "/tmp/repo-task",
			"-e", "RIG_TASK_ID=task-2", "-e", "CLAUDE_CONFIG_DIR=/home/me/.claude-work",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:s2", "claude", "C-m",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		ID:           "task-2",
		ParentID:     "task-1",
		TmuxSession:  "repo_task",
		TmuxWindow:   "s2",
		WorktreePath: "/tmp/repo-task",
		ProviderEnv:  core.ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-work"},
	}, core.TaskSessionLaunchSpec{Command: []string{"claude"}, ReadyMarker: "❯"})

	require.NoError(t, err, "the window carries the child's own task ID so its hooks are told apart")
}

func TestRepositoryStartTaskSession_ReusesAChildTasksExistingWindow(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"has-session", "-t", "=repo_task",
		),
		expectTmuxRun(runner, subprocess.Result{Stdout: "task\ns2\n"}, nil,
			"list-windows", "-t", "=repo_task", "-F", "#{window_name}",
		),
		expectTmuxRun(runner, subprocess.Result{}, nil,
			"send-keys", "-t", "=repo_task:s2", "claude --resume sess-2", "C-m",
		),
	)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:  "repo_task",
		TmuxWindow:   "s2",
		WorktreePath: "/tmp/repo-task",
	}, core.TaskSessionLaunchSpec{Command: []string{"claude", "--resume", "sess-2"}})

	require.NoError(t, err)
}

func TestRepositoryAttachAndDeleteTaskSession_TargetAChildTasksWindow(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	insideTmux(repo, nil)
	child := &core.Task{TmuxSession: "repo_task", TmuxWindow: "s2"}

	expectTmuxRun(runner, subprocess.Result{}, nil, "switch-client", "-t", "=repo_task:s2")
	expectTmuxRun(runner, subprocess.Result{Stdout: "rig"}, nil, "display-message", "-p", "-t", "%3", "#{session_name}")
	runner.On("Run", mock.Anything, "", "tmux", "set-option", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(subprocess.Result{}, nil).
		Maybe()
	runner.On("Run", mock.Anything, "", "tmux", "set-option", mock.Anything, mock.Anything, mock.Anything).
		Return(subprocess.Result{}, nil).Maybe()
	runner.On("Run", mock.Anything, "", "tmux", "bind-key", mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything).Return(subprocess.Result{}, nil).Maybe()
	require.NoError(t, repo.AttachTaskSession(context.Background(), child))

	expectTmuxRun(runner, subprocess.Result{}, nil, "kill-window", "-t", "=repo_task:s2")
	require.NoError(
		t,
		repo.DeleteTaskSession(context.Background(), child),
		"only the window: the session is the parent's",
	)
}

func TestRepositorySubmitTaskInput_TypesTheTextThenPressesEnterAfterASettle(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)
	var slept []time.Duration
	repo.sleep = func(d time.Duration) { slept = append(slept, d) }

	mock.InOrder(
		expectTmuxRun(runner, subprocess.Result{}, nil, "send-keys", "-t", "=repo_task:s2", "-l", "/exit"),
		expectTmuxRun(runner, subprocess.Result{}, nil, "send-keys", "-t", "=repo_task:s2", "Enter"),
	)

	err := repo.SubmitTaskInput(context.Background(), &core.Task{TmuxSession: "repo_task", TmuxWindow: "s2"}, "/exit")

	require.NoError(t, err)
	require.Equal(t, []time.Duration{promptInputSettleDelay}, slept,
		"the provider's command suggestion must be up before Enter runs it")
}

func TestRepositoryInspectTaskSessions_TellsAChildTasksWindowFromItsParents(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	repo := New(runner).(*repository)

	expectTmuxRun(runner, subprocess.Result{
		Stdout: "repo_task\ttask\tzsh\t100\nrepo_task\ts2\t2.1.291\t200\nrepo_task\teditor\tnvim\t300\n",
	}, nil, "list-panes", "-a", "-F", "#{session_name}\t#{window_name}\t#{pane_current_command}\t#{pane_pid}")
	runner.On("Run", mock.Anything, "", "ps", "-axo", "ppid=,comm=").
		Return(subprocess.Result{Stdout: "200 claude\n300 nvim\n"}, nil).Once()

	states, err := repo.InspectTaskSessions(context.Background(), []*core.Task{
		{ID: "task-1", TmuxSession: "repo_task"},
		{ID: "task-2", TmuxSession: "repo_task", TmuxWindow: "s2"},
		{ID: "task-3", TmuxSession: "repo_task", TmuxWindow: "s3"},
	})

	require.NoError(t, err)
	require.True(t, states["task-1"].Exists)
	require.Equal(t, []string{"zsh"}, states["task-1"].ActiveCommands, "the parent's pane is idle")
	require.True(t, states["task-2"].Exists)
	require.Equal(t, []string{"2.1.291", "claude"}, states["task-2"].ActiveCommands, "the child's runs claude")
	require.False(t, states["task-3"].Exists, "a window that is gone")
}
