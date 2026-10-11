package subprocess

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fastWaits shortens Close's waits so tests of its escalation stay fast.
var fastWaits = lineSessionWaits{
	stdinEOF:   100 * time.Millisecond,
	terminate:  100 * time.Millisecond,
	exitStatus: defaultLineSessionWaits.exitStatus,
}

func startShellSession(t *testing.T, ctx context.Context, script string) LineSession {
	t.Helper()
	session, err := startLineSession(ctx, StartLineSessionOptions{
		Name: "sh",
		Args: []string{"-c", script},
	}, fastWaits)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestExecRunnerLineSession_RoundTripsLinesWhileStdinStaysOpen(t *testing.T) {
	session, err := ExecRunner{}.StartLineSession(t.Context(), StartLineSessionOptions{
		Name: "sh",
		Args: []string{"-c", `while IFS= read -r line; do printf 'echo:%s\n' "$line"; done`},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	require.NoError(t, session.Send("first"))
	line, err := session.Receive()
	require.NoError(t, err)
	require.Equal(t, "echo:first", line)

	require.NoError(t, session.Send(`{"id":2}`))
	line, err = session.Receive()
	require.NoError(t, err)
	require.Equal(t, `echo:{"id":2}`, line)

	require.NoError(t, session.Close())
}

func TestExecRunnerLineSession_ReceivesLinesLongerThanTheScannerDefault(t *testing.T) {
	session := startShellSession(t, t.Context(), "exec cat")
	long := strings.Repeat("x", 1<<20)

	require.NoError(t, session.Send(long))
	line, err := session.Receive()

	require.NoError(t, err)
	require.Equal(t, long, line)
}

func TestExecRunnerLineSession_ReceiveReturnsEOFAfterTheProcessExits(t *testing.T) {
	session := startShellSession(t, t.Context(), "echo only")

	line, err := session.Receive()
	require.NoError(t, err)
	require.Equal(t, "only", line)

	_, err = session.Receive()
	require.ErrorIs(t, err, io.EOF)
}

// The session owns its stdout pipe, so lines the process wrote before it
// exited are still there to read.
func TestExecRunnerLineSession_KeepsLinesWrittenBeforeTheProcessExited(t *testing.T) {
	session := startShellSession(t, t.Context(), `printf 'a\nb\nc\n'; exit 2`)
	time.Sleep(200 * time.Millisecond)

	for _, want := range []string{"a", "b", "c"} {
		line, err := session.Receive()
		require.NoError(t, err)
		require.Equal(t, want, line)
	}
	_, err := session.Receive()

	require.ErrorIs(t, err, io.EOF)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 2, exitErr.ExitCode())
}

func TestExecRunnerLineSession_EOFErrorCarriesExitStatusAndStderr(t *testing.T) {
	session := startShellSession(t, t.Context(), "echo 'not logged in' >&2; exit 3")

	_, err := session.Receive()

	require.ErrorIs(t, err, io.EOF)
	require.ErrorContains(t, err, "exit status 3")
	require.ErrorContains(t, err, "not logged in")
}

func TestExecRunnerLineSession_CloseTerminatesAProcessThatIgnoresStdinEOF(t *testing.T) {
	session := startShellSession(t, t.Context(), "exec sleep 30")
	start := time.Now()

	err := session.Close()

	require.NoError(t, err)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestExecRunnerLineSession_CloseKillsAProcessThatIgnoresTerminate(t *testing.T) {
	session := startShellSession(t, t.Context(), `trap '' TERM; echo ready; exec sleep 30`)
	line, err := session.Receive()
	require.NoError(t, err)
	require.Equal(t, "ready", line)
	start := time.Now()

	err = session.Close()

	require.NoError(t, err)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestExecRunnerLineSession_ReceiveAfterCloseReturnsEOF(t *testing.T) {
	session := startShellSession(t, t.Context(), "exec cat")
	require.NoError(t, session.Close())

	_, err := session.Receive()

	require.ErrorIs(t, err, io.EOF)
	require.EqualError(t, err, io.EOF.Error())
}

func TestExecRunnerLineSession_ReceiveReportsTheContextEndingTheSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	session := startShellSession(t, ctx, "exec sleep 30")
	start := time.Now()

	_, err := session.Receive()

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotErrorIs(t, err, io.EOF)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestExecRunnerLineSession_StartReportsAMissingBinary(t *testing.T) {
	_, err := ExecRunner{}.StartLineSession(t.Context(), StartLineSessionOptions{Name: "rig-no-such-binary"})

	require.ErrorContains(t, err, "rig-no-such-binary")
}

func TestTailBuffer_KeepsTheLatestBytes(t *testing.T) {
	buf := &tailBuffer{limit: 5}

	_, _ = buf.Write([]byte("abc"))
	_, _ = buf.Write([]byte("defg"))

	require.Equal(t, "cdefg", buf.String())
}
