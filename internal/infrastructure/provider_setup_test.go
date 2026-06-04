package infrastructure

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestProviderSetupSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig", "config.json")
	setup := ProviderSetup{
		ConfiguredProviders: []core.Provider{core.ProviderClaude, core.ProviderCodex, core.ProviderCodex},
		DefaultProvider:     core.ProviderClaude,
	}

	require.NoError(t, SaveProviderSetup(path, setup))

	loaded, err := LoadProviderSetup(path)
	require.NoError(t, err)
	require.Equal(t, []core.Provider{core.ProviderClaude, core.ProviderCodex}, loaded.ConfiguredProviders)
	require.Equal(t, core.ProviderClaude, loaded.DefaultProvider)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestProviderSetupValidationRejectsZeroConfiguredProviders(t *testing.T) {
	err := ProviderSetup{DefaultProvider: core.ProviderCodex}.Validate()

	require.ErrorIs(t, err, ErrProviderSetupRequired)
}

func TestProviderSetupValidationRejectsInvalidDefaultProvider(t *testing.T) {
	err := ProviderSetup{
		ConfiguredProviders: []core.Provider{core.ProviderCodex},
		DefaultProvider:     core.ProviderClaude,
	}.Validate()

	require.Error(t, err)
	require.Contains(t, err.Error(), "default provider")
}

func TestProviderSetupValidationRejectsUnknownProviders(t *testing.T) {
	err := ProviderSetup{
		ConfiguredProviders: []core.Provider{core.Provider("gemini")},
		DefaultProvider:     core.Provider("gemini"),
	}.Validate()

	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown provider")
}

func TestLoadProviderSetupRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))

	setup, err := LoadProviderSetup(path)
	require.Empty(t, setup)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid provider setup")
}
