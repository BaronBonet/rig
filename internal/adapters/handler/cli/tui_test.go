package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRootCommand_ReturnsErrorWhenServiceNotConfigured(t *testing.T) {
	cmd := NewRootCommand(Dependencies{})
	cmd.SetIn(bytes.NewBufferString("q"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.EqualError(t, err, "service not configured")
}
