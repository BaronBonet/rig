package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskStatusIsTerminal_BrokenIsTerminal(t *testing.T) {
	require.True(t, TaskStatusBroken.IsTerminal())
	require.False(t, TaskStatusRunning.IsTerminal())
}
