package providerregistry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// TestProviderModulesMatchSupportedProviders keeps the registry's composition
// list and core's domain-level provider list in agreement: a provider added
// to one but not the other fails here instead of silently missing clients,
// routes, or setup support.
func TestProviderModulesMatchSupportedProviders(t *testing.T) {
	var moduleProviders []core.Provider
	for _, module := range providerModules {
		moduleProviders = append(moduleProviders, module.provider)
		require.NotNil(t, module.client, "provider %s must contribute a client constructor", module.provider)
		require.NotNil(t, module.routes, "provider %s must contribute hook routes", module.provider)
	}

	require.ElementsMatch(t, core.SupportedProviders(), moduleProviders)
}

func TestLoadOrCreateHookSecret_PersistsAcrossCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig", "hook-secret")

	first, err := LoadOrCreateHookSecret(path)
	require.NoError(t, err)
	require.Len(t, first, 64)

	second, err := LoadOrCreateHookSecret(path)
	require.NoError(t, err)
	require.Equal(t, first, second)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLoadOrCreateHookSecret_RegeneratesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hook-secret")
	require.NoError(t, os.WriteFile(path, []byte(" \n"), 0o600))

	secret, err := LoadOrCreateHookSecret(path)
	require.NoError(t, err)
	require.Len(t, secret, 64)
}
