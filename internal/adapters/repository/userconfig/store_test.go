package userconfig

import (
	"context"
	"encoding/json"
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

const configWithAccounts = `{
  "version": 1,
  "configured_providers": ["claude"],
  "default_provider": "claude",
  "accounts": [
    {
      "provider": "claude",
      "name": "work",
      "dir": "~/.claude-work/",
      "folders": ["~/work/code/", "", "  ", "/srv/../srv/shared"]
    },
    {"provider": "codex", "name": " personal ", "dir": "~"},
    {"provider": "claude", "name": "elsewhere", "dir": "/opt/claude/../claude-x/"}
  ]
}`

// useHome makes the process's home directory a new temporary one.
func useHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeConfig(t *testing.T, path string, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// writtenAccounts returns the accounts array of the config file as written.
func writtenAccounts(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var file struct {
		Accounts []map[string]any `json:"accounts"`
	}
	require.NoError(t, json.Unmarshal(raw, &file))
	return file.Accounts
}

func TestStore_GetAccountsExpandsHomeAndCleansPaths(t *testing.T) {
	home := useHome(t)
	store, path := newTestStore(t, "")
	writeConfig(t, path, configWithAccounts)

	accounts, err := store.GetAccounts(t.Context())

	require.NoError(t, err)
	require.Len(t, accounts, 3)
	require.Equal(t, core.AccountConfig{
		Provider: core.ProviderClaude,
		Name:     "work",
		Dir:      filepath.Join(home, ".claude-work"),
		Folders:  []string{filepath.Join(home, "work", "code"), "/srv/shared"},
	}, accounts[0], "blank folders are dropped and the others expanded and cleaned")
	require.Equal(t, core.ProviderCodex, accounts[1].Provider)
	require.Equal(t, "personal", accounts[1].Name, "names are trimmed")
	require.Equal(t, home, accounts[1].Dir, "a lone ~ is the home directory")
	require.Empty(t, accounts[1].Folders)
	require.Equal(t, "/opt/claude-x", accounts[2].Dir, "a dir outside the home is only cleaned")
}

func TestStore_GetAccountsKeepsTheConfigAsTheUserWroteIt(t *testing.T) {
	useHome(t)
	store, path := newTestStore(t, "")
	writeConfig(t, path, configWithAccounts)

	_, err := store.GetAccounts(t.Context())

	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.JSONEq(t, configWithAccounts, string(raw))
}

func TestStore_GetAccountsWithoutAConfigFileOffersNoAccounts(t *testing.T) {
	store, path := newTestStore(t, "")

	accounts, err := store.GetAccounts(t.Context())

	require.NoError(t, err)
	require.Empty(t, accounts)
	require.NoFileExists(t, path)
}

func TestStore_GetAccountsWithoutAnAccountsEntryOffersNoAccounts(t *testing.T) {
	store, _ := newTestStore(t, "")
	require.NoError(t, store.SaveProviderSetup(t.Context(), core.ProviderSetup{
		Configured: []core.Provider{core.ProviderClaude},
		Default:    core.ProviderClaude,
	}))

	accounts, err := store.GetAccounts(t.Context())

	require.NoError(t, err)
	require.Empty(t, accounts)
}

func TestStore_GetAccountsRejectsAnAccountWithoutAProviderNameOrDir(t *testing.T) {
	tests := []struct {
		name    string
		account string
	}{
		{name: "no name", account: `{"provider":"claude","dir":"~/.claude-work"}`},
		{name: "a blank name", account: `{"provider":"claude","name":"  ","dir":"~/.claude-work"}`},
		{name: "no dir", account: `{"provider":"claude","name":"work"}`},
		{name: "a blank dir", account: `{"provider":"claude","name":"work","dir":" "}`},
		{name: "no provider", account: `{"name":"work","dir":"~/.claude-work"}`},
		{name: "an unsupported provider", account: `{"provider":"gemini","name":"work","dir":"~/.gemini"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useHome(t)
			store, path := newTestStore(t, "")
			writeConfig(t, path, `{"version":1,"configured_providers":["claude"],"default_provider":"claude",`+
				`"accounts":[{"provider":"codex","name":"fine","dir":"~/.codex-x"},`+test.account+`]}`)

			accounts, err := store.GetAccounts(t.Context())

			require.ErrorContains(t, err, "invalid account 2")
			require.Nil(t, accounts)
		})
	}
}

func TestStore_GetAccountsFailsOnAnUnreadableConfig(t *testing.T) {
	store, path := newTestStore(t, "")
	writeConfig(t, path, `{`)

	_, err := store.GetAccounts(t.Context())

	require.ErrorContains(t, err, "decode user config")
}

func TestStore_GetAccountsNeedsAHomeOnlyForPathsThatStartWithOne(t *testing.T) {
	t.Setenv("HOME", "")
	store, path := newTestStore(t, "")
	writeConfig(t, path, `{"accounts":[{"provider":"claude","name":"x","dir":"/opt/claude-x"}]}`)

	accounts, err := store.GetAccounts(t.Context())
	require.NoError(t, err)
	require.Equal(t, "/opt/claude-x", accounts[0].Dir)

	writeConfig(t, path, `{"accounts":[{"provider":"claude","name":"x","dir":"~/.claude-x"}]}`)
	_, err = store.GetAccounts(t.Context())
	require.ErrorContains(t, err, "resolve home directory")

	writeConfig(t, path, `{"accounts":[{"provider":"claude","name":"x","dir":"/opt/x","folders":["~/code"]}]}`)
	_, err = store.GetAccounts(t.Context())
	require.ErrorContains(t, err, "resolve home directory")
}

func TestExpandHome(t *testing.T) {
	home := useHome(t)

	tests := []struct {
		path string
		want string
	}{
		{path: "~", want: home},
		{path: "~/", want: home},
		{path: "~/.claude-work", want: filepath.Join(home, ".claude-work")},
		{path: "~/a/../b/", want: filepath.Join(home, "b")},
		{path: "  ~/work  ", want: filepath.Join(home, "work")},
		{path: "/abs//path/", want: "/abs/path"},
		{path: "relative/x/", want: "relative/x"},
		{path: "~other/x", want: "~other/x"},
		{path: "/not/~/home", want: "/not/~/home"},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			got, err := expandHome(test.path)

			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestStore_AccountsSurviveProviderSetupAndLaunchDefaults(t *testing.T) {
	useHome(t)
	store, path := newTestStore(t, "")
	ctx := t.Context()
	writeConfig(t, path, configWithAccounts)
	var original struct {
		Accounts []map[string]any `json:"accounts"`
	}
	require.NoError(t, json.Unmarshal([]byte(configWithAccounts), &original))
	require.Len(t, original.Accounts, 3)

	// Rerunning provider setup keeps them, as the user wrote them.
	require.NoError(t, store.SaveProviderSetup(ctx, core.ProviderSetup{
		Configured: []core.Provider{core.ProviderClaude, core.ProviderCodex},
		Default:    core.ProviderCodex,
	}))
	require.Equal(t, original.Accounts, writtenAccounts(t, path))

	// So does recording what a provider was last launched with.
	require.NoError(t, store.SaveLaunchDefaults(ctx, core.ProviderClaude, core.LaunchOptions{Model: "opus"}))
	require.Equal(t, original.Accounts, writtenAccounts(t, path))
	require.NoError(t, store.SaveLaunchDefaults(ctx, core.ProviderClaude, core.LaunchOptions{}))
	require.Equal(t, original.Accounts, writtenAccounts(t, path))

	accounts, err := store.GetAccounts(ctx)
	require.NoError(t, err)
	require.Len(t, accounts, 3)
	setup, err := store.GetProviderSetup(ctx)
	require.NoError(t, err)
	require.Equal(t, core.ProviderCodex, setup.Default)
}
