package cli

import (
	"bytes"
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
		ListTasks(mock.Anything).
		Return([]*core.Task{}, nil).
		Once()

	cmd := NewRootCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetIn(strings.NewReader("q"))
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
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
