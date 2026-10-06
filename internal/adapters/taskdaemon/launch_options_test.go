package taskdaemon

import (
	"context"
	"errors"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestGetLaunchSettingsRoundTrip(t *testing.T) {
	t.Parallel()

	svc := newFakeTaskService()
	svc.launchSettings = &core.LaunchSettings{
		Options: map[core.Provider]core.ProviderLaunchOptions{
			core.ProviderClaude: {Models: []string{"opus"}, Efforts: []string{"max"}, Handoff: true},
		},
		Defaults: map[core.Provider]core.LaunchOptions{core.ProviderClaude: {Model: "opus"}},
	}
	client := startTestFrontend(t, svc)

	settings, err := client.GetLaunchSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, svc.launchSettings, settings)

	svc.mu.Lock()
	svc.errByOp["get_launch_settings"] = errors.New("config unreadable")
	svc.mu.Unlock()
	_, err = client.GetLaunchSettings(context.Background())
	require.ErrorContains(t, err, "config unreadable")
}

func TestNewTaskSessionStreamRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("streams progress before the terminal task", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createSteps = []core.TaskCreateProgressStep{
			core.TaskCreateProgressWritingHandoff,
			core.TaskCreateProgressStartingSession,
		}
		svc.createTask = &core.Task{ID: "task-1", DisplayName: "add retries", Model: "opus"}
		client := startTestFrontend(t, svc)

		events, err := client.NewTaskSessionStream(context.Background(), core.NewTaskSessionInput{
			TaskID:  "task-1",
			Prompt:  "continue",
			Model:   "opus",
			Effort:  "max",
			Handoff: true,
		})
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		require.Len(t, got, 3)
		require.Equal(t, core.TaskCreateProgressWritingHandoff, got[0].Progress.Step)
		require.Equal(t, core.TaskCreateProgressStartingSession, got[1].Progress.Step)
		require.NotNil(t, got[2].Task)
		require.Equal(t, "opus", got[2].Task.Model)
		svc.mu.Lock()
		require.Equal(t, &core.NewTaskSessionInput{
			TaskID:  "task-1",
			Prompt:  "continue",
			Model:   "opus",
			Effort:  "max",
			Handoff: true,
		}, svc.newSessionInput)
		svc.mu.Unlock()
	})

	t.Run("requires a task id", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		events, err := client.NewTaskSessionStream(context.Background(), core.NewTaskSessionInput{Prompt: "continue"})
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		require.Len(t, got, 1)
		require.ErrorContains(t, got[0].Err, "task ID required")
	})
}
