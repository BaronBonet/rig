package userconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func saveTestSetup(t *testing.T, store core.UserConfigStore) core.ProviderSetup {
	t.Helper()
	setup := core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderCodex,
	}
	require.NoError(t, store.SaveProviderSetup(context.Background(), setup))
	return setup
}

func TestStore_LaunchDefaultsRoundTrip(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()
	saveTestSetup(t, store)

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderClaude, core.LaunchChoice{
		Model:          "opus[1m]",
		ReasoningLevel: "high",
	}))
	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{ReasoningLevel: "low"}))

	defaults, err := store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchChoice{
		core.ProviderClaude: {Model: "opus[1m]", ReasoningLevel: "high"},
		core.ProviderCodex:  {ReasoningLevel: "low"},
	}, defaults)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"version": 1,
		"configured_providers": ["codex", "claude"],
		"default_provider": "codex",
		"launch_defaults": {
			"claude": {"model": "opus[1m]", "reasoning_level": "high"},
			"codex": {"reasoning_level": "low"}
		}
	}`, string(raw))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestStore_SaveProviderSetupKeepsTheLaunchDefaults(t *testing.T) {
	store, _ := newTestStore(t, "")
	ctx := context.Background()
	saveTestSetup(t, store)
	choice := core.LaunchChoice{Model: "opus[1m]", ReasoningLevel: "high"}
	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderClaude, choice))

	next := core.ProviderSetup{Configured: []core.Provider{core.ProviderClaude}, Default: core.ProviderClaude}
	require.NoError(t, store.SaveProviderSetup(ctx, next))

	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, &next, setup)
	defaults, err := store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchChoice{core.ProviderClaude: choice}, defaults)
}

func TestStore_SaveLaunchDefaultKeepsTheProviderSetup(t *testing.T) {
	store, _ := newTestStore(t, "")
	ctx := context.Background()
	saved := saveTestSetup(t, store)

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{Model: "gpt-5.5"}))

	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, &saved, setup)
}

// The runtime default-provider override is applied when reading the setup,
// and a Launch default save must not write it to disk.
func TestStore_SaveLaunchDefaultDoesNotPersistTheDefaultProviderOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	saveTestSetup(t, New(Config{Path: path}, ""))
	overridden := New(Config{Path: path}, core.ProviderClaude)

	require.NoError(t, overridden.SaveLaunchDefault(
		context.Background(),
		core.ProviderCodex,
		core.LaunchChoice{Model: "gpt-5.5"},
	))

	setup, err := New(Config{Path: path}, "").GetProviderSetup(context.Background())
	require.NoError(t, err)
	require.Equal(t, core.ProviderCodex, setup.Default)
}

func TestStore_SaveLaunchDefaultWithAnEmptyChoiceDeletesTheEntry(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()
	saveTestSetup(t, store)
	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{Model: "gpt-5.5"}))
	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderClaude, core.LaunchChoice{Model: "opus"}))

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{}))

	defaults, err := store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchChoice{core.ProviderClaude: {Model: "opus"}}, defaults)

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderClaude, core.LaunchChoice{}))
	defaults, err = store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Empty(t, defaults)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "launch_defaults")
}

// Before Provider setup completes there is no config file, and no valid one
// to create: Launch defaults read as none, and saving one saves nothing.
func TestStore_LaunchDefaultsWithoutAConfigFile(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()

	defaults, err := store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.NotNil(t, defaults)
	require.Empty(t, defaults)

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{Model: "gpt-5.5"}))
	require.NoFileExists(t, path)
}

func TestStore_LoadsAConfigWrittenBeforeLaunchDefaults(t *testing.T) {
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
	defaults, err := store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Empty(t, defaults)

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{Model: "gpt-5.5"}))
	defaults, err = store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchChoice{core.ProviderCodex: {Model: "gpt-5.5"}}, defaults)
}

// An edit by hand can leave entries Rig cannot use. Each is skipped on its
// own, and none of them stops the Provider setup from loading, which task
// creation needs.
func TestStore_GetLaunchDefaultsSkipsEntriesItCannotUse(t *testing.T) {
	store, path := newTestStore(t, "")
	require.NoError(t, os.WriteFile(path, []byte(`{
		"version": 1,
		"configured_providers": ["codex"],
		"default_provider": "codex",
		"launch_defaults": {
			"gemini": {"model": "gemini-3"},
			"claude": "opus",
			"codex": {"model": "gpt-5.5", "reasoning_level": "high"}
		}
	}`), 0o600))

	defaults, err := store.GetLaunchDefaults(context.Background())

	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-5.5", ReasoningLevel: "high"},
	}, defaults)
	setup, err := store.GetProviderSetup(context.Background())
	require.NoError(t, err)
	require.Equal(t, core.ProviderCodex, setup.Default)
}

func TestStore_LaunchDefaultsThatAreNotAnObjectHoldNone(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"version":1,"configured_providers":["codex"],"default_provider":"codex","launch_defaults":"x"}`),
		0o600,
	))

	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, core.ProviderCodex, setup.Default)
	defaults, err := store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Empty(t, defaults)

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{Model: "gpt-5.5"}))
	defaults, err = store.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchChoice{core.ProviderCodex: {Model: "gpt-5.5"}}, defaults)
}

// A newer rig, or an edit by hand, can add fields and entries this rig does
// not know. Saves write them back as they were.
func TestStore_SavesKeepFieldsAndEntriesTheyDoNotKnow(t *testing.T) {
	store, path := newTestStore(t, "")
	ctx := context.Background()
	require.NoError(t, os.WriteFile(path, []byte(`{
		"version": 1,
		"configured_providers": ["codex"],
		"default_provider": "codex",
		"extra": {"theme": "dark"},
		"launch_defaults": {
			"gemini": {"model": "gemini-3"},
			"claude": "opus"
		}
	}`), 0o600))

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{Model: "gpt-5.5"}))
	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderClaude,
	}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"version": 1,
		"configured_providers": ["codex", "claude"],
		"default_provider": "claude",
		"extra": {"theme": "dark"},
		"launch_defaults": {
			"gemini": {"model": "gemini-3"},
			"claude": "opus",
			"codex": {"model": "gpt-5.5"}
		}
	}`, string(raw))
}

// A dotfiles setup links the config path to a file elsewhere. A save writes
// through the link rather than replacing it.
func TestStore_SavesWriteThroughASymlinkedConfig(t *testing.T) {
	target := filepath.Join(t.TempDir(), "rig-config.json")
	require.NoError(t, os.WriteFile(
		target,
		[]byte(`{"version":1,"configured_providers":["codex"],"default_provider":"codex"}`),
		0o600,
	))
	link := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.Symlink(target, link))
	store := New(Config{Path: link}, "")
	ctx := context.Background()

	require.NoError(t, store.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{Model: "gpt-5.5"}))
	saved := saveTestSetup(t, store)

	info, err := os.Lstat(link)
	require.NoError(t, err)
	require.Equal(t, os.ModeSymlink, info.Mode().Type())
	resolved, err := os.Readlink(link)
	require.NoError(t, err)
	require.Equal(t, target, resolved)

	targetStore := New(Config{Path: target}, "")
	setup, err := targetStore.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, &saved, setup)
	defaults, err := targetStore.GetLaunchDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[core.Provider]core.LaunchChoice{core.ProviderCodex: {Model: "gpt-5.5"}}, defaults)
	targetInfo, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), targetInfo.Mode().Perm())
}

// Only Provider setup repairs a broken config. A Launch default save, which
// task creation makes, leaves it exactly as it was.
func TestStore_SaveLaunchDefaultLeavesAnUndecodableConfigAlone(t *testing.T) {
	store, path := newTestStore(t, "")
	broken := []byte(`{"version":1,"configured_providers":["codex"]`)
	require.NoError(t, os.WriteFile(path, broken, 0o600))

	err := store.SaveLaunchDefault(context.Background(), core.ProviderCodex, core.LaunchChoice{Model: "gpt-5.5"})

	require.ErrorContains(t, err, "decode user config")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, broken, raw)
}

func TestStore_SaveLaunchDefaultRejectsUnsupportedProviders(t *testing.T) {
	store, _ := newTestStore(t, "")
	saveTestSetup(t, store)

	err := store.SaveLaunchDefault(context.Background(), core.Provider("gemini"), core.LaunchChoice{Model: "gemini-3"})

	require.ErrorContains(t, err, `provider "gemini" is not a supported provider`)
}

// Provider setup repairs a config file that does not decode, as it did before
// Launch defaults existed.
func TestStore_SaveProviderSetupReplacesAnUndecodableConfig(t *testing.T) {
	store, path := newTestStore(t, "")
	require.NoError(t, os.WriteFile(path, []byte(`{`), 0o600))

	saved := saveTestSetup(t, store)

	setup, err := store.GetProviderSetup(context.Background())
	require.NoError(t, err)
	require.Equal(t, &saved, setup)
}

// The daemon may save Provider setup and Launch defaults at once. Each save
// rewrites the whole file, so without serializing them one would undo another.
func TestStore_ConcurrentSavesKeepEveryUpdate(t *testing.T) {
	store, _ := newTestStore(t, "")
	ctx := context.Background()
	setup := saveTestSetup(t, store)

	const rounds = 20
	for round := range rounds {
		codex := core.LaunchChoice{Model: fmt.Sprintf("gpt-%d", round)}
		claude := core.LaunchChoice{ReasoningLevel: fmt.Sprintf("level-%d", round)}

		var wg sync.WaitGroup
		errs := make(chan error, 3)
		wg.Go(func() { errs <- store.SaveLaunchDefault(ctx, core.ProviderCodex, codex) })
		wg.Go(func() { errs <- store.SaveLaunchDefault(ctx, core.ProviderClaude, claude) })
		wg.Go(func() { errs <- store.SaveProviderSetup(ctx, setup) })
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}

		defaults, err := store.GetLaunchDefaults(ctx)
		require.NoError(t, err)
		require.Equal(t, map[core.Provider]core.LaunchChoice{
			core.ProviderCodex:  codex,
			core.ProviderClaude: claude,
		}, defaults, "round %d", round)
		got, err := store.GetProviderSetup(ctx)
		require.NoError(t, err)
		require.Equal(t, &setup, got)
	}
}
