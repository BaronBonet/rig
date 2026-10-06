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
