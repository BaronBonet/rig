//go:build unix

package observer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTryLockFile_BlocksSecondDescriptor(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "observer.startup.lock")

	first, err := openObserverStartupLockFile(lockPath)
	require.NoError(t, err)
	defer first.Close()

	second, err := openObserverStartupLockFile(lockPath)
	require.NoError(t, err)
	defer second.Close()

	locked, err := tryLockFile(first)
	require.NoError(t, err)
	require.True(t, locked)

	locked, err = tryLockFile(second)
	require.NoError(t, err)
	require.False(t, locked)

	require.NoError(t, unlockFile(first))

	locked, err = tryLockFile(second)
	require.NoError(t, err)
	require.True(t, locked)

	require.NoError(t, unlockFile(second))
	require.NoError(t, os.Remove(lockPath))
}
