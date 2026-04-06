package cli

import (
	"bytes"
	"strings"
	"testing"

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
	service := &fakeTUIService{}

	cmd := NewRootCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetIn(strings.NewReader("q"))
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, 1, service.listCalls)
}
