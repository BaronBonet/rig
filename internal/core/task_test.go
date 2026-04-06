package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskStatusIsTerminal_BrokenIsTerminal(t *testing.T) {
	require.True(t, TaskStatusBroken.IsTerminal())
	require.False(t, TaskStatusRunning.IsTerminal())
	require.False(t, TaskStatusDegraded.IsTerminal())
}

func TestCorePublicTypesRemainUsable(t *testing.T) {
	task := Task{
		DisplayName: "billing retry flow",
		Status:      TaskStatusRunning,
		Provider:    "codex",
	}

	require.Equal(t, "billing retry flow", task.DisplayName)
	require.False(t, task.Status.IsTerminal())
}
