package userconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func newTestStore(t *testing.T, defaultProviderOverride core.Provider) (core.ProviderConfigStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	return New(Config{Path: path}, defaultProviderOverride), path
}

func TestStore_GetProviderSetupReturnsNilWhenConfigIsMissing(t *testing.T) {
	store, _ := newTestStore(t, "")

	setup, err := store.GetProviderSetup(context.Background())

	require.NoError(t, err)
	require.Nil(t, setup)
}

func TestStore_SaveThenGetRoundTrips(t *testing.T) {
	store, path := newTestStore(t, "")
	saved := core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderClaude,
	}

	require.NoError(t, store.SaveProviderSetup(context.Background(), saved))

	setup, err := store.GetProviderSetup(context.Background())
	require.NoError(t, err)
	require.Equal(t, &saved, setup)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestStore_SaveRejectsInvalidSetups(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()

	require.ErrorContains(t,
		store.SaveProviderSetup(ctx, core.ProviderSetup{}),
		"at least one configured provider",
	)
	require.ErrorContains(t,
		store.SaveProviderSetup(ctx, core.ProviderSetup{
			Configured: []core.Provider{core.Provider("gemini")},
			Default:    core.Provider("gemini"),
		}),
		`provider "gemini" is not a supported provider`,
	)
	require.ErrorContains(t,
		store.SaveProviderSetup(ctx, core.ProviderSetup{
			Configured: []core.Provider{core.ProviderCodex},
			Default:    core.ProviderClaude,
		}),
		`default provider "claude" is not a configured provider`,
	)
	require.NoFileExists(t, path)
}

func TestStore_GetRejectsInvalidPersistedConfig(t *testing.T) {
	store, path := newTestStore(t, "")

	require.NoError(t, os.WriteFile(path, []byte(`{`), 0o600))
	_, err := store.GetProviderSetup(context.Background())
	require.ErrorContains(t, err, "decode user config")

	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":[],"default_provider":""}`),
		0o600,
	))
	_, err = store.GetProviderSetup(context.Background())
	require.ErrorContains(t, err, "at least one configured provider")

	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":["gemini"],"default_provider":"gemini"}`),
		0o600,
	))
	_, err = store.GetProviderSetup(context.Background())
	require.ErrorContains(t, err, `provider "gemini" is not a supported provider`)

	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":["codex"],"default_provider":"claude"}`),
		0o600,
	))
	_, err = store.GetProviderSetup(context.Background())
	require.ErrorContains(t, err, `default provider "claude" is not a configured provider`)
}

func TestStore_DefaultProviderOverrideMustBeConfigured(t *testing.T) {
	store, path := newTestStore(t, core.ProviderClaude)
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":["codex"],"default_provider":"codex"}`),
		0o600,
	))

	_, err := store.GetProviderSetup(context.Background())

	require.ErrorContains(t, err, `RIG_PROVIDER "claude" is not a configured provider`)
}

func TestStore_DefaultProviderOverrideSelectsConfiguredProvider(t *testing.T) {
	store, path := newTestStore(t, core.ProviderClaude)
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":["codex","claude"],"default_provider":"codex"}`),
		0o600,
	))

	setup, err := store.GetProviderSetup(context.Background())

	require.NoError(t, err)
	require.Equal(t, core.ProviderClaude, setup.Default)
}

func TestStore_SaveIsIncremental(t *testing.T) {
	store, _ := newTestStore(t, "")
	ctx := context.Background()

	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex},
		Default:    core.ProviderCodex,
	}))
	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderClaude,
	}))

	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, []core.Provider{core.ProviderCodex, core.ProviderClaude}, setup.Configured)
	require.Equal(t, core.ProviderClaude, setup.Default)
}
