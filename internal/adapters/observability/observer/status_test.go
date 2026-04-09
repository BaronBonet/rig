package observer

import (
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestDeriveDisplayStatus_PrefersFinished(t *testing.T) {
	status := DeriveDisplayStatus(StatusInput{
		TaskStatus:    core.TaskStatusCleaned,
		RuntimeState:  core.RuntimeStateRunning,
		ProcessAlive:  true,
		ActiveCommand: true,
	})

	require.Equal(t, core.DisplayStatusFinished, status.Primary)
	require.Equal(t, core.DisplayActivityNone, status.Activity)
}

func TestDeriveDisplayStatus_PrefersNeedsInputOverHookCommandActivity(t *testing.T) {
	status := DeriveDisplayStatus(StatusInput{
		TaskStatus:    core.TaskStatusRunning,
		RuntimeState:  core.RuntimeStateNeedsInput,
		ProcessAlive:  true,
		ActiveCommand: true,
	})

	require.Equal(t, core.DisplayStatusNeedsInput, status.Primary)
	require.Equal(t, core.DisplayActivityNone, status.Activity)
}

func TestDeriveDisplayStatus_AddsCommandDetailOnlyWhenWorking(t *testing.T) {
	working := DeriveDisplayStatus(StatusInput{
		TaskStatus:    core.TaskStatusRunning,
		RuntimeState:  core.RuntimeStateRunning,
		ProcessAlive:  true,
		ActiveCommand: true,
	})
	require.Equal(t, core.DisplayStatusWorking, working.Primary)
	require.Equal(t, core.DisplayActivityCommand, working.Activity)

	notWorking := DeriveDisplayStatus(StatusInput{
		TaskStatus:    core.TaskStatusRunning,
		RuntimeState:  core.RuntimeStateRunning,
		ProcessAlive:  true,
		ActiveCommand: false,
	})
	require.Equal(t, core.DisplayStatusWorking, notWorking.Primary)
	require.Equal(t, core.DisplayActivityNone, notWorking.Activity)
}

func TestDeriveDisplayStatus_ReturnsDisconnectedWhenProcessMissing(t *testing.T) {
	status := DeriveDisplayStatus(StatusInput{
		TaskStatus:    core.TaskStatusRunning,
		RuntimeState:  core.RuntimeStateRunning,
		ProcessAlive:  false,
		ActiveCommand: true,
	})

	require.Equal(t, core.DisplayStatusDisconnected, status.Primary)
	require.Equal(t, core.DisplayActivityNone, status.Activity)
}
