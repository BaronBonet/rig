package tmux

import (
	"context"
	"errors"
	"testing"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRepository_StartTaskSession_LaunchesCommandAndTypesInitialInput(t *testing.T) {
	runner := &fakeRunner{
		paneContent: "›",
	}
	repo := NewRepository(runner)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:      "repo_task",
		WorktreePath:     "/tmp/repo-task",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	}, core.LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"fix billing retry flow"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"codex"}, runner.sentCommand)
	require.Equal(t, []string{"fix billing retry flow"}, runner.typedCommand)
}

func TestRepositoryCreateSession_UsesDetachedSessionInWorkingDir(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.CreateSession(context.Background(), core.CreateSessionInput{
		SessionName:      "repo-billing-retry-flow",
		WorkingDir:       "/tmp/repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})
	require.NoError(t, err)
	require.Len(t, runner.Calls, 2)
	require.Equal(t, "tmux", runner.Calls[0].Name)
	require.Equal(t, []string{
		"new-session",
		"-d",
		"-s",
		"repo-billing-retry-flow",
		"-n",
		"agent",
		"-c",
		"/tmp/repo-billing-retry-flow",
	}, runner.Calls[0].Args)
	require.Equal(t, "tmux", runner.Calls[1].Name)
	require.Equal(t, []string{
		"new-window",
		"-d",
		"-t",
		"=repo-billing-retry-flow",
		"-n",
		"editor",
		"-c",
		"/tmp/repo-billing-retry-flow",
	}, runner.Calls[1].Args)
}

func TestRepositoryCreateSession_KillsSessionIfEditorWindowCreationFails(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}, {}, {}})
	runner.Errors = []error{nil, errors.New("new-window failed"), nil}
	repo := NewRepository(runner)

	err := repo.CreateSession(context.Background(), core.CreateSessionInput{
		SessionName:      "repo-billing-retry-flow",
		WorkingDir:       "/tmp/repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})

	require.EqualError(t, err, "new-window failed")
	require.Len(t, runner.Calls, 3)
	require.Equal(t, []string{
		"kill-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[2].Args)
}

func TestRepositoryCreateSession_JoinsWindowCreationAndCleanupErrors(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}, {}, {}})
	runner.Errors = []error{nil, errors.New("new-window failed"), errors.New("kill-session failed")}
	repo := NewRepository(runner)

	err := repo.CreateSession(context.Background(), core.CreateSessionInput{
		SessionName:      "repo-billing-retry-flow",
		WorkingDir:       "/tmp/repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})

	require.Error(t, err)
	require.ErrorContains(t, err, "new-window failed")
	require.ErrorContains(t, err, "kill-session failed")
	require.Len(t, runner.Calls, 3)
}

func TestRepositoryOpenTaskSession_UsesTaskSession(t *testing.T) {
	t.Setenv("TMUX", "")

	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.OpenTaskSession(context.Background(), &core.Task{TmuxSession: "repo-billing-retry-flow"})
	require.NoError(t, err)
	require.Equal(t, []string{
		"attach-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositoryDeleteTaskSession_UsesTaskSession(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.DeleteTaskSession(context.Background(), &core.Task{TmuxSession: "repo-billing-retry-flow"})
	require.NoError(t, err)
	require.Equal(t, []string{
		"kill-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositoryInspectTaskSession_ReturnsSessionAndWindowResources(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{},
		{Stdout: "agent\neditor\n"},
		{Stdout: "agent\neditor\n"},
	})
	repo := NewRepository(runner)

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
	repo := NewRepository(execx.NewFakeRunner(nil))
	repo.runtimeMonitor = &fakeRuntimeMonitor{
		snapshot: core.RuntimeSnapshot{
			SessionName: "repo-billing-retry-flow",
			PaneID:      "%24",
		},
	}

	snapshot, err := repo.SnapshotTaskSession(context.Background(), &core.Task{TmuxSession: "repo-billing-retry-flow"})
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", snapshot.SessionName)
	require.Equal(t, "%24", snapshot.PaneID)
	require.Equal(t, "repo-billing-retry-flow", repo.runtimeMonitor.(*fakeRuntimeMonitor).task.TmuxSession)
}

func TestRepositoryNormalizesColonSessionNamesAcrossTmuxCommands(t *testing.T) {
	t.Setenv("TMUX", "")

	runner := execx.NewFakeRunner([]execx.Result{
		{},
		{},
		{},
		{Stdout: "agent\neditor\n"},
		{},
		{},
		{},
	})
	repo := NewRepository(runner)

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

	require.Len(t, runner.Calls, 7)
	require.Equal(t, []string{
		"new-session",
		"-d",
		"-s",
		"repo-billing-retry-flow",
		"-n",
		"agent",
		"-c",
		"/tmp/repo-billing-retry-flow",
	}, runner.Calls[0].Args)
	require.Equal(t, []string{
		"new-window",
		"-d",
		"-t",
		"=repo-billing-retry-flow",
		"-n",
		"editor",
		"-c",
		"/tmp/repo-billing-retry-flow",
	}, runner.Calls[1].Args)
	require.Equal(t, []string{
		"has-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[2].Args)
	require.Equal(t, []string{
		"list-windows",
		"-t",
		"=repo-billing-retry-flow",
		"-F",
		"#{window_name}",
	}, runner.Calls[3].Args)
	require.Equal(t, []string{
		"attach-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[4].Args)
	require.Equal(t, []string{
		"send-keys",
		"-t",
		"=repo-billing-retry-flow:editor",
		"codex 'fix bug'",
		"C-m",
	}, runner.Calls[5].Args)
	require.Equal(t, []string{
		"kill-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[6].Args)
}

func TestRepositoryNormalizesColonSessionNamesForSwitchClientInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-123/default,123,0")

	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	require.NoError(t, repo.AttachOrSwitch(context.Background(), "repo:billing-retry-flow"))
	require.Equal(t, []string{
		"switch-client",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositorySendKeysToWindow_UsesNamedWindowTarget(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.SendKeysToWindow(
		context.Background(),
		"repo-billing-retry-flow",
		"editor",
		[]string{"codex", "fix bug"},
	)
	require.NoError(t, err)
	require.Equal(t, []string{
		"send-keys",
		"-t",
		"=repo-billing-retry-flow:editor",
		"codex 'fix bug'",
		"C-m",
	}, runner.Calls[0].Args)
}

func TestRepositorySendKeysToWindow_DefaultsEmptyWindowToAgent(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.SendKeysToWindow(context.Background(), "repo-billing-retry-flow", "", []string{"codex", "fix bug"})
	require.NoError(t, err)
	require.Equal(t, []string{
		"send-keys",
		"-t",
		"=repo-billing-retry-flow:agent",
		"codex 'fix bug'",
		"C-m",
	}, runner.Calls[0].Args)
}

func TestRepositoryTypeInWindow_SendsKeysWithoutEnter(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.TypeInWindow(
		context.Background(),
		"repo-billing-retry-flow",
		"agent",
		[]string{"codex", "fix bug"},
	)
	require.NoError(t, err)
	require.Equal(t, []string{
		"send-keys",
		"-t",
		"=repo-billing-retry-flow:agent",
		"codex 'fix bug'",
	}, runner.Calls[0].Args)
}

func TestRepositoryAttachOrSwitch_UsesExactSessionTarget(t *testing.T) {
	t.Setenv("TMUX", "")

	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.AttachOrSwitch(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, []string{
		"attach-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositoryAttachOrSwitch_UsesSwitchClientInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-123/default,123,0")

	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.AttachOrSwitch(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, []string{
		"switch-client",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositorySessionExists_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	exists, err := repo.SessionExists(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, []string{
		"has-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositorySessionExists_ReturnsFalseForMissingSessionOnly(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{Stderr: "can't find session: repo-billing-retry-flow\n"}})
	runner.Errors = []error{execx.CommandError{
		Name:   "tmux",
		Args:   []string{"has-session", "-t", "=repo-billing-retry-flow"},
		Stderr: "can't find session: repo-billing-retry-flow\n",
		Err:    errors.New("exit status 1"),
	}}
	repo := NewRepository(runner)

	exists, err := repo.SessionExists(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestRepositorySessionExists_ReturnsErrorForTmuxFailure(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{Stderr: "failed to connect to server\n"}})
	runner.Errors = []error{execx.CommandError{
		Name:   "tmux",
		Args:   []string{"has-session", "-t", "=repo-billing-retry-flow"},
		Stderr: "failed to connect to server\n",
		Err:    errors.New("exit status 1"),
	}}
	repo := NewRepository(runner)

	exists, err := repo.SessionExists(context.Background(), "repo-billing-retry-flow")
	require.Error(t, err)
	require.False(t, exists)
}

func TestRepositoryWindowExists_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{Stdout: "agent\neditor\n"}})
	repo := NewRepository(runner)

	exists, err := repo.WindowExists(context.Background(), "repo-billing-retry-flow", "agent")
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, []string{
		"list-windows",
		"-t",
		"=repo-billing-retry-flow",
		"-F",
		"#{window_name}",
	}, runner.Calls[0].Args)
}

func TestRepositoryWindowExists_ReturnsFalseForMissingSessionOnly(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{Stderr: "can't find session: repo-billing-retry-flow\n"}})
	runner.Errors = []error{execx.CommandError{
		Name:   "tmux",
		Args:   []string{"list-windows", "-t", "=repo-billing-retry-flow", "-F", "#{window_name}"},
		Stderr: "can't find session: repo-billing-retry-flow\n",
		Err:    errors.New("exit status 1"),
	}}
	repo := NewRepository(runner)

	exists, err := repo.WindowExists(context.Background(), "repo-billing-retry-flow", "agent")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestRepositoryKillSession_UsesExactSessionTarget(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.KillSession(context.Background(), "repo-billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, []string{
		"kill-session",
		"-t",
		"=repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}

type fakeRunner struct {
	sentCommand  []string
	typedCommand []string
	paneContent  string
}

type fakeRuntimeMonitor struct {
	task     *core.Task
	snapshot core.RuntimeSnapshot
	err      error
}

func (f *fakeRuntimeMonitor) Snapshot(_ context.Context, task *core.Task) (core.RuntimeSnapshot, error) {
	f.task = task
	return f.snapshot, f.err
}

func (f *fakeRuntimeMonitor) Close() error { return nil }

func (f *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) (execx.Result, error) {
	if name != "tmux" {
		return execx.Result{}, nil
	}

	if len(args) == 0 {
		return execx.Result{}, nil
	}

	switch args[0] {
	case "send-keys":
		if len(args) < 4 {
			return execx.Result{}, nil
		}

		command := splitTmuxCommand(args[3])
		if len(args) > 4 && args[4] == "C-m" {
			f.sentCommand = append([]string(nil), command...)
		} else {
			f.typedCommand = append([]string(nil), command...)
		}
	case "capture-pane":
		return execx.Result{Stdout: f.paneContent}, nil
	}

	return execx.Result{}, nil
}

func (f *fakeRunner) RunWithStdin(context.Context, execx.RunWithStdinOptions) (execx.Result, error) {
	return execx.Result{}, nil
}

func splitTmuxCommand(raw string) []string {
	switch raw {
	case "codex":
		return []string{"codex"}
	case "'fix billing retry flow'":
		return []string{"fix billing retry flow"}
	default:
		return []string{raw}
	}
}
