//go:build unix

package subprocess

import (
	"errors"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A child the process started in the background is in its process group, so
// it neither keeps Close waiting nor outlives the session.
func TestExecRunnerLineSession_CloseStopsTheProcessGroup(t *testing.T) {
	session := startShellSession(t, t.Context(), `sleep 30 & echo $!; sleep 30`)
	line, err := session.Receive()
	require.NoError(t, err)
	backgroundPID, err := strconv.Atoi(line)
	require.NoError(t, err)
	start := time.Now()

	err = session.Close()

	require.NoError(t, err)
	require.Less(t, time.Since(start), 2*time.Second)
	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(backgroundPID, 0), syscall.ESRCH)
	}, 2*time.Second, 20*time.Millisecond)
}
