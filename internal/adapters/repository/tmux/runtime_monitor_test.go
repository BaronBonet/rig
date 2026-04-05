package tmux

import (
	"context"
	"io"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRuntimeMonitorSnapshot_BindsOnlyCodexPaneInSplitAgentWindow(t *testing.T) {
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex\n%31\tzsh",
			"capture-pane -t %24 -p -e": "› review my changes\nWorking (26s • esc to interrupt)\n",
		},
		lastOutputAt: time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC),
	}
	monitor := NewRuntimeMonitorWithFactory(&fakeControlPipeFactory{
		pipes: map[string]controlPipe{"repo-billing-retry-flow": pipe},
	}, func() time.Time {
		return time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	})

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.Equal(t, "codex", snapshot.ForegroundCommand)
	require.Equal(t, "repo-billing-retry-flow", snapshot.SessionName)
	require.Equal(t, "agent", snapshot.WindowName)
	require.Equal(t, "› review my changes\nWorking (26s • esc to interrupt)\n", snapshot.Content)
	require.Equal(t, time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC), snapshot.ObservedAt)
	require.Equal(t, time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC), snapshot.LastOutputAt)
}

func TestRuntimeMonitorSnapshot_ReturnsEmptyWhenMultipleCodexPanesExist(t *testing.T) {
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex\n%31\tcodex",
		},
	}
	monitor := NewRuntimeMonitorWithFactory(&fakeControlPipeFactory{
		pipes: map[string]controlPipe{"repo-billing-retry-flow": pipe},
	}, time.Now)

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
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex",
			"capture-pane -t %24 -p -e": "› review my changes\nWorking (26s • esc to interrupt)\n",
		},
	}
	monitor := NewRuntimeMonitorWithFactory(&fakeControlPipeFactory{
		pipes: map[string]controlPipe{"repo-billing-retry-flow": pipe},
	}, time.Now)

	first, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", first.PaneID)
	require.Equal(t, "codex", first.ForegroundCommand)

	pipe.output["list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}"] = "%24\tzsh"
	pipe.output["capture-pane -t %24 -p -e"] = "done\n"

	second, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", second.PaneID)
	require.Equal(t, "zsh", second.ForegroundCommand)
	require.Equal(t, "done\n", second.Content)
}

func TestRuntimeMonitorSnapshot_UsesLatestLastOutputAt(t *testing.T) {
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex",
			"capture-pane -t %24 -p -e": "› review my changes\n",
		},
		lastOutputAt: time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC),
	}
	monitor := NewRuntimeMonitorWithFactory(&fakeControlPipeFactory{
		pipes: map[string]controlPipe{"repo-billing-retry-flow": pipe},
	}, func() time.Time {
		return time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	})

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC), snapshot.LastOutputAt)

	pipe.lastOutputAt = time.Date(2026, 4, 5, 10, 0, 2, 0, time.UTC)
	snapshot, err = monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 4, 5, 10, 0, 2, 0, time.UTC), snapshot.LastOutputAt)
}

func TestRuntimeMonitor_CloseClosesBoundControlPipes(t *testing.T) {
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex",
			"capture-pane -t %24 -p -e": "› review my changes\n",
		},
	}
	monitor := NewRuntimeMonitorWithFactory(&fakeControlPipeFactory{
		pipes: map[string]controlPipe{"repo-billing-retry-flow": pipe},
	}, time.Now)

	_, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)

	require.NoError(t, monitor.Close())
	require.True(t, pipe.closed)
}

func TestRuntimeMonitorSnapshot_EvictsDeadPipeAndReattaches(t *testing.T) {
	firstPipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex",
		},
		errors: map[string]error{
			"capture-pane -t %24 -p -e": io.ErrClosedPipe,
		},
	}
	secondPipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex",
			"capture-pane -t %24 -p -e": "› review my changes\n",
		},
	}
	factory := &fakeControlPipeFactory{
		queue: []controlPipe{firstPipe, secondPipe},
	}
	monitor := NewRuntimeMonitorWithFactory(factory, time.Now)

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.Error(t, err)
	require.Empty(t, snapshot.PaneID)
	require.True(t, firstPipe.closed)
	require.Len(t, factory.calls, 1)

	snapshot, err = monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.Equal(t, "› review my changes\n", snapshot.Content)
	require.Len(t, factory.calls, 2)
	require.False(t, secondPipe.closed)
}

type fakeControlPipe struct {
	output       map[string]string
	errors       map[string]error
	lastOutputAt time.Time
	closed       bool
}

func (f *fakeControlPipe) SendCommand(command string) (string, error) {
	if err, ok := f.errors[command]; ok {
		return "", err
	}
	return f.output[command], nil
}

func (f *fakeControlPipe) LastOutputAt() time.Time {
	return f.lastOutputAt
}

func (f *fakeControlPipe) Close() error {
	f.closed = true
	return nil
}

type fakeControlPipeFactory struct {
	pipes map[string]controlPipe
	queue []controlPipe
	calls []string
}

func (f *fakeControlPipeFactory) Attach(session string) (controlPipe, error) {
	f.calls = append(f.calls, session)
	if len(f.queue) > 0 {
		pipe := f.queue[0]
		f.queue = f.queue[1:]
		return pipe, nil
	}
	if pipe, ok := f.pipes[session]; ok {
		return pipe, nil
	}
	return &fakeControlPipe{output: map[string]string{}}, nil
}
