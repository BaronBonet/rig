package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRootCommand_HelpIncludesSubcommands(t *testing.T) {
	out := &bytes.Buffer{}

	cmd := NewRootCommand(Dependencies{})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	require.Contains(t, output, "new")
	require.Contains(t, output, "ls")
	require.Contains(t, output, "open")
	require.Contains(t, output, "status")
	require.Contains(t, output, "doctor")
}
