package tmux

import (
	"context"
	"io"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPaneListCommand_QuotesFormatForTmuxControlMode(t *testing.T) {
	require.Equal(
		t,
		`list-panes -t =repo-billing-retry-flow:agent -F "#{pane_id}\t#{pane_current_command}\t#{pane_active}"`,
		paneListCommand("repo-billing-retry-flow", "agent"),
	)
}

func TestRuntimeMonitorSnapshot_BindsOnlyCodexPaneInSplitAgentWindow(t *testing.T) {
	lastOutputAt := time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC)
	pipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tcodex\t1\n%31\tzsh\t0",
		"capture-pane -t %24 -p -e":                         "› review my changes\nWorking (26s • esc to interrupt)\n",
	}, nil, &lastOutputAt, nil)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), func() time.Time {
		return time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	})

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.True(t, snapshot.HadAgentBinding)
	require.Equal(t, "codex", snapshot.ForegroundCommand)
	require.Equal(t, "repo-billing-retry-flow", snapshot.SessionName)
	require.Equal(t, "agent", snapshot.WindowName)
	require.Equal(t, "› review my changes\nWorking (26s • esc to interrupt)\n", snapshot.Content)
	require.Equal(t, time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC), snapshot.ObservedAt)
	require.Equal(t, time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC), snapshot.LastOutputAt)
}

func TestRuntimeMonitorSnapshot_BindsCodexAliasPaneInSplitAgentWindow(t *testing.T) {
	lastOutputAt := time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC)
	pipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tcodex-aarch64-a\t1\n%31\tzsh\t0",
		"capture-pane -t %24 -p -e":                         "› review my changes\n",
	}, nil, &lastOutputAt, nil)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), func() time.Time {
		return time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	})

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.True(t, snapshot.HadAgentBinding)
	require.Equal(t, "codex", snapshot.ForegroundCommand)
}

func TestRuntimeMonitorSnapshot_ReturnsEmptyWhenMultipleCodexPanesExist(t *testing.T) {
	pipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tcodex\t1\n%31\tcodex\t0",
	}, nil, nil, nil)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), time.Now)

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Empty(t, snapshot.PaneID)
	require.Empty(t, snapshot.ForegroundCommand)
	require.Empty(t, snapshot.Content)
}

func TestRuntimeMonitorSnapshot_ReusesBoundPaneAfterCodexReturnsToShell(t *testing.T) {
	output := map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tcodex\t1",
		"capture-pane -t %24 -p -e":                         "› review my changes\nWorking (26s • esc to interrupt)\n",
	}
	pipe := newMockControlPipe(t, output, nil, nil, nil)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), time.Now)

	first, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", first.PaneID)
	require.True(t, first.HadAgentBinding)
	require.Equal(t, "codex", first.ForegroundCommand)

	output[paneListCommand("repo-billing-retry-flow", "agent")] = "%24\tzsh\t1"
	output["capture-pane -t %24 -p -e"] = "done\n"

	second, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", second.PaneID)
	require.True(t, second.HadAgentBinding)
	require.Equal(t, "zsh", second.ForegroundCommand)
	require.Equal(t, "done\n", second.Content)
}

func TestRuntimeMonitorSnapshot_PreservesShellOnlyHistoryAcrossRepeatedObservations(t *testing.T) {
	pipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tzsh\t1",
		"capture-pane -t %24 -p -e":                         "done\n",
	}, nil, nil, nil)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), time.Now)

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.False(t, snapshot.HadAgentBinding)
	require.Equal(t, "zsh", snapshot.ForegroundCommand)

	snapshot, err = monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.False(t, snapshot.HadAgentBinding)
	require.Equal(t, "zsh", snapshot.ForegroundCommand)
}

func TestRuntimeMonitorSnapshot_MarksCodexHistoryAfterPaneTransitionsFromShellToCodex(t *testing.T) {
	output := map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tzsh\t1",
		"capture-pane -t %24 -p -e":                         "done\n",
	}
	pipe := newMockControlPipe(t, output, nil, nil, nil)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), time.Now)

	first, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.False(t, first.HadAgentBinding)

	output[paneListCommand("repo-billing-retry-flow", "agent")] = "%24\tcodex\t1"
	output["capture-pane -t %24 -p -e"] = "› review my changes\nWorking (26s • esc to interrupt)\n"

	second, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.True(t, second.HadAgentBinding)
	require.Equal(t, "codex", second.ForegroundCommand)

	output[paneListCommand("repo-billing-retry-flow", "agent")] = "%24\tzsh\t1"
	output["capture-pane -t %24 -p -e"] = "done again\n"

	third, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.True(t, third.HadAgentBinding)
	require.Equal(t, "zsh", third.ForegroundCommand)
}

func TestRuntimeMonitorSnapshot_UsesLatestLastOutputAt(t *testing.T) {
	lastOutputAt := time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC)
	pipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tcodex\t1",
		"capture-pane -t %24 -p -e":                         "› review my changes\n",
	}, nil, &lastOutputAt, nil)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), func() time.Time {
		return time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	})

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC), snapshot.LastOutputAt)

	lastOutputAt = time.Date(2026, 4, 5, 10, 0, 2, 0, time.UTC)
	snapshot, err = monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 4, 5, 10, 0, 2, 0, time.UTC), snapshot.LastOutputAt)
}

func TestRuntimeMonitor_CloseClosesBoundControlPipes(t *testing.T) {
	closed := false
	pipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tcodex\t1",
		"capture-pane -t %24 -p -e":                         "› review my changes\n",
	}, nil, nil, &closed)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), time.Now)

	_, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)

	require.NoError(t, monitor.Close())
	require.True(t, closed)
}

func TestRuntimeMonitorSnapshot_EvictsDeadPipeAndReattaches(t *testing.T) {
	firstClosed := false
	firstPipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tcodex\t1",
	}, map[string]error{
		"capture-pane -t %24 -p -e": io.ErrClosedPipe,
	}, nil, &firstClosed)
	secondClosed := false
	secondPipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tcodex\t1",
		"capture-pane -t %24 -p -e":                         "› review my changes\n",
	}, nil, nil, &secondClosed)
	var calls []string
	factory := newMockControlPipeFactory(t, nil, []controlPipe{firstPipe, secondPipe}, &calls)
	monitor := NewRuntimeMonitorWithFactory(factory, time.Now)

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.Error(t, err)
	require.Empty(t, snapshot.PaneID)
	require.True(t, firstClosed)
	require.Len(t, calls, 1)

	snapshot, err = monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.Equal(t, "› review my changes\n", snapshot.Content)
	require.Len(t, calls, 2)
	require.False(t, secondClosed)
}

func TestRuntimeMonitorSnapshot_BindsActiveShellPaneAsFinishedFallback(t *testing.T) {
	pipe := newMockControlPipe(t, map[string]string{
		paneListCommand("repo-billing-retry-flow", "agent"): "%24\tzsh\t1\n%31\tzsh\t0",
		"capture-pane -t %24 -p -e":                         "done\n",
	}, nil, nil, nil)
	monitor := NewRuntimeMonitorWithFactory(newMockControlPipeFactory(t, map[string]controlPipe{
		"repo-billing-retry-flow": pipe,
	}, nil, nil), time.Now)

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.True(t, snapshot.HadAgentBinding)
	require.Equal(t, "zsh", snapshot.ForegroundCommand)
}

func newMockControlPipe(
	t *testing.T,
	output map[string]string,
	errors map[string]error,
	lastOutputAt *time.Time,
	closed *bool,
) *MockcontrolPipe {
	t.Helper()

	pipe := NewMockcontrolPipe(t)
	pipe.On("SendCommand", mock.Anything).Return(
		func(command string) string {
			return output[command]
		},
		func(command string) error {
			if errors == nil {
				return nil
			}

			return errors[command]
		},
	).Maybe()
	pipe.On("LastOutputAt").Return(func() time.Time {
		if lastOutputAt == nil {
			return time.Time{}
		}

		return *lastOutputAt
	}).Maybe()
	pipe.On("Close").Return(func() error {
		if closed != nil {
			*closed = true
		}

		return nil
	}).Maybe()

	return pipe
}

func newMockControlPipeFactory(
	t *testing.T,
	pipes map[string]controlPipe,
	queue []controlPipe,
	calls *[]string,
) *MockcontrolPipeFactory {
	t.Helper()

	emptyPipe := newMockControlPipe(t, map[string]string{}, nil, nil, nil)
	remaining := append([]controlPipe(nil), queue...)
	factory := NewMockcontrolPipeFactory(t)
	factory.On("Attach", mock.Anything).Return(
		func(session string) controlPipe {
			if calls != nil {
				*calls = append(*calls, session)
			}
			if len(remaining) > 0 {
				pipe := remaining[0]
				remaining = remaining[1:]

				return pipe
			}
			if pipe, ok := pipes[session]; ok {
				return pipe
			}

			return emptyPipe
		},
		func(string) error { return nil },
	).Maybe()

	return factory
}
