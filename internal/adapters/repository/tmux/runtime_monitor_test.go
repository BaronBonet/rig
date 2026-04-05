package tmux

import (
	"context"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRuntimeMonitorSnapshot_BindsOnlyCodexPaneInSplitAgentWindow(t *testing.T) {
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex\n%31\tzsh",
			"capture-pane -t %24 -p -e":                                                   "› review my changes\nWorking (26s • esc to interrupt)\n",
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
			"capture-pane -t %24 -p -e":                                                   "› review my changes\nWorking (26s • esc to interrupt)\n",
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
			"capture-pane -t %24 -p -e":                                                   "› review my changes\n",
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
			"capture-pane -t %24 -p -e":                                                   "› review my changes\n",
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

type fakeControlPipe struct {
	output       map[string]string
	lastOutputAt time.Time
	closed       bool
}

func (f *fakeControlPipe) SendCommand(command string) (string, error) {
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
}

func (f *fakeControlPipeFactory) Attach(session string) (controlPipe, error) {
	if pipe, ok := f.pipes[session]; ok {
		return pipe, nil
	}
	return &fakeControlPipe{output: map[string]string{}}, nil
}
