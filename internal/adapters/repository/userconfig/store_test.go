package userconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func newTestStore(t *testing.T, defaultProviderOverride core.Provider) (core.UserConfigStore, string) {
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

func TestStore_GetRepoDirsReturnsNoneWhenConfigIsMissing(t *testing.T) {
	store, _ := newTestStore(t, "")

	dirs, err := store.GetRepoDirs(context.Background())

	require.NoError(t, err)
	require.Nil(t, dirs)
}

func TestStore_SaveRepoDirsThenGetRoundTrips(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()
	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex},
		Default:    core.ProviderCodex,
	}))

	require.NoError(t, store.SaveRepoDirs(ctx, []string{"/work", "/Users/me/code"}))

	dirs, err := store.GetRepoDirs(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/work", "/Users/me/code"}, dirs)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestStore_SaveNoRepoDirsLeavesNone(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":["codex"],"default_provider":"codex","repo_dirs":["/work"]}`),
		0o600,
	))

	require.NoError(t, store.SaveRepoDirs(ctx, nil))

	dirs, err := store.GetRepoDirs(ctx)
	require.NoError(t, err)
	require.Empty(t, dirs)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "repo_dirs")
}

// Configs written before Rig had repo directories keep working without them.
func TestStore_LoadsAConfigWithoutRepoDirs(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":["codex"],"default_provider":"codex"}`),
		0o600,
	))

	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(
		t,
		&core.ProviderSetup{Configured: []core.Provider{core.ProviderCodex}, Default: core.ProviderCodex},
		setup,
	)
	dirs, err := store.GetRepoDirs(ctx)
	require.NoError(t, err)
	require.Nil(t, dirs)
}

func TestStore_SaveProviderSetupKeepsTheRepoDirs(t *testing.T) {
	store, _ := newTestStore(t, "")
	ctx := context.Background()
	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex},
		Default:    core.ProviderCodex,
	}))
	require.NoError(t, store.SaveRepoDirs(ctx, []string{"/work"}))

	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderClaude,
	}))

	dirs, err := store.GetRepoDirs(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/work"}, dirs)
}

// Setup is how a broken config gets fixed, so saving a provider setup
// replaces a config that cannot be decoded.
func TestStore_SaveProviderSetupReplacesAConfigThatCannotBeDecoded(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()
	require.NoError(t, os.WriteFile(path, []byte(`{`), 0o600))
	saved := core.ProviderSetup{Configured: []core.Provider{core.ProviderCodex}, Default: core.ProviderCodex}

	require.NoError(t, store.SaveProviderSetup(ctx, saved))

	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, &saved, setup)
}

func TestStore_SaveRepoDirsKeepsTheProviderSetup(t *testing.T) {
	store, _ := newTestStore(t, "")
	ctx := context.Background()
	saved := core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderClaude,
	}
	require.NoError(t, store.SaveProviderSetup(ctx, saved))

	require.NoError(t, store.SaveRepoDirs(ctx, []string{"/work"}))

	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, &saved, setup)
}

// RIG_PROVIDER overrides the default provider only at runtime: saving the
// repo directories keeps the saved default provider.
func TestStore_SaveRepoDirsKeepsTheSavedDefaultProviderUnderAnOverride(t *testing.T) {
	store, path := newTestStore(t, core.ProviderClaude)
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":["codex","claude"],"default_provider":"codex"}`),
		0o600,
	))

	require.NoError(t, store.SaveRepoDirs(context.Background(), []string{"/work"}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"default_provider": "codex"`)
}

// A config without providers would make provider setup look invalid rather
// than never completed, so repo directories are never saved before it.
func TestStore_SaveRepoDirsRequiresProviderSetup(t *testing.T) {
	store, path := newTestStore(t, "")

	err := store.SaveRepoDirs(context.Background(), []string{"/work"})

	require.ErrorIs(t, err, core.ErrProviderSetupRequired)
	require.NoFileExists(t, path)
}

func TestStore_SaveRepoDirsRejectsAnInvalidConfig(t *testing.T) {
	store, path := newTestStore(t, "")
	invalid := []byte(`{"version":1,"configured_providers":[],"default_provider":""}`)
	require.NoError(t, os.WriteFile(path, invalid, 0o600))

	err := store.SaveRepoDirs(context.Background(), []string{"/work"})

	require.ErrorContains(t, err, "at least one configured provider")
	raw, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, invalid, raw)
}

func TestStore_GetRepoDirsRejectsAConfigThatCannotBeDecoded(t *testing.T) {
	store, path := newTestStore(t, "")
	require.NoError(t, os.WriteFile(path, []byte(`{`), 0o600))

	_, err := store.GetRepoDirs(context.Background())

	require.ErrorContains(t, err, "decode user config")
}

func TestStore_SaveRepoDirsLeavesAConfigThatCannotBeDecoded(t *testing.T) {
	store, path := newTestStore(t, "")
	require.NoError(t, os.WriteFile(path, []byte(`{`), 0o600))

	err := store.SaveRepoDirs(context.Background(), []string{"/work"})

	require.ErrorContains(t, err, "decode user config")
	raw, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, []byte(`{`), raw)
}
