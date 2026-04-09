package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewRootCommand_HelpOnlyIncludesDoctorSubcommand(t *testing.T) {
	out := &bytes.Buffer{}

	cmd := NewRootCommand(Dependencies{})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	require.Contains(t, output, "doctor")
	require.NotContains(t, output, "new")
	require.NotContains(t, output, "ls")
	require.NotContains(t, output, "open")
	require.NotContains(t, output, "status")
	require.NotContains(t, output, "tui")
}

func TestNewRootCommand_RunsTUIWhenNoArgsProvided(t *testing.T) {
	out := &bytes.Buffer{}
	service := NewMockTaskService(t)
	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return([]*core.TaskView{}, nil).
		Maybe()

	cmd := NewRootCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  out,
	})
	cmd.SetIn(strings.NewReader("q"))
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
}

func TestNewRootCommand_StartsObserverBeforeLaunchingTUI(t *testing.T) {
	out := &bytes.Buffer{}
	service := NewMockTaskService(t)
	observer := &stubObserverProcess{}

	service.EXPECT().
		ListTaskViews(mock.Anything).
		Run(func(context.Context) {
			require.True(t, observer.started)
		}).
		Return([]*core.TaskView{}, nil).
		Maybe()

	cmd := NewRootCommand(Dependencies{
		Service:         service,
		Stdout:          out,
		Stderr:          out,
		ObserverProcess: observer,
	})
	cmd.SetIn(strings.NewReader("q"))
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.True(t, observer.started)
}

func TestNewRootCommand_ContinuesWhenObserverStartupFails(t *testing.T) {
	out := &bytes.Buffer{}
	service := NewMockTaskService(t)
	observer := &stubObserverProcess{err: errors.New("observer unavailable")}

	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return([]*core.TaskView{}, nil).
		Maybe()

	cmd := NewRootCommand(Dependencies{
		Service:         service,
		Stdout:          out,
		Stderr:          out,
		ObserverProcess: observer,
	})
	cmd.SetIn(strings.NewReader("q"))
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.True(t, observer.started)
}

func TestNewRootCommand_DoctorDispatchBypassesRootTUI(t *testing.T) {
	out := &bytes.Buffer{}
	service := NewMockTaskService(t)
	service.EXPECT().
		Doctor(mock.Anything, mock.Anything).
		Return(core.DoctorResult{Notes: []string{"doctor: ok"}}, nil).
		Once()

	cmd := NewRootCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"doctor"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "doctor: ok")
}

func TestNewRootCommand_ObserverIngestDispatchesToConfiguredIngestor(t *testing.T) {
	out := &bytes.Buffer{}
	ingestor := &stubHookEventIngestor{}

	cmd := NewRootCommand(Dependencies{Stdout: out, Stderr: out, HookIngestor: ingestor})
	cmd.SetIn(strings.NewReader(`{"cwd":"/tmp/worktree","session_id":"sess-1","hook_event_name":"SessionStart"}`))
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"observer", "ingest", "SessionStart"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, "SessionStart", ingestor.input.EventName)
	require.Equal(t, "/tmp/worktree", ingestor.input.Cwd)
	require.Equal(t, "sess-1", ingestor.input.SessionID)
}

type stubHookEventIngestor struct {
	input core.HookEventInput
}

func (s *stubHookEventIngestor) IngestHookEvent(_ context.Context, input core.HookEventInput) (*core.HookSessionSummary, error) {
	s.input = input
	return nil, nil
}

type stubObserverProcess struct {
	started bool
	err     error
}

func (s *stubObserverProcess) EnsureRunning(context.Context) error {
	s.started = true
	return s.err
}
