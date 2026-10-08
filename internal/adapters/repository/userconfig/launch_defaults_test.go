package userconfig

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func TestStore_LaunchDefaultsRoundTripPerProvider(t *testing.T) {
	store, _ := newTestStore(t, "")
	ctx := context.Background()
	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderClaude},
		Default:    core.ProviderClaude,
	}))

	defaults, err := store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Empty(t, defaults)

	require.NoError(
		t,
		store.SaveLaunchDefaults(ctx, core.ProviderClaude, core.LaunchOptions{Model: " opus ", Effort: "max"}),
	)
	require.NoError(t, store.SaveLaunchDefaults(ctx, core.ProviderCodex, core.LaunchOptions{Model: "gpt-5"}))

	defaults, err = store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchOptions{
		core.ProviderClaude: {Model: "opus", Effort: "max"},
		core.ProviderCodex:  {Model: "gpt-5"},
	}, defaults)

	// Blank options drop the provider's entry rather than storing nothing.
	require.NoError(t, store.SaveLaunchDefaults(ctx, core.ProviderCodex, core.LaunchOptions{}))
	defaults, err = store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(
		t,
		map[core.Provider]core.LaunchOptions{core.ProviderClaude: {Model: "opus", Effort: "max"}},
		defaults,
	)
}

func TestStore_LaunchDefaultsSurviveProviderSetupAndNeedIt(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()

	err := store.SaveLaunchDefaults(ctx, core.ProviderClaude, core.LaunchOptions{Model: "opus"})
	require.ErrorIs(t, err, core.ErrProviderSetupRequired)
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "launch defaults never create a config on their own")

	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderClaude},
		Default:    core.ProviderClaude,
	}))
	require.NoError(t, store.SaveLaunchDefaults(ctx, core.ProviderClaude, core.LaunchOptions{Model: "opus"}))

	// Rerunning provider setup keeps them.
	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderClaude, core.ProviderCodex},
		Default:    core.ProviderCodex,
	}))
	defaults, err := store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchOptions{core.ProviderClaude: {Model: "opus"}}, defaults)
	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, core.ProviderCodex, setup.Default)
}
