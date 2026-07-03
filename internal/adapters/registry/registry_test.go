package registry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
