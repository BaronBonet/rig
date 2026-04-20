package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessConfig_OnlyExposesRuntimeWiringFields(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(ProcessConfig{})
	var got []string
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}

	require.Equal(t, []string{
		"SocketPath",
		"ExecPath",
		"Env",
	}, got)
}

func TestStop_RequiresSocketPath(t *testing.T) {
	t.Parallel()

	err := Stop(context.Background(), "")
	require.EqualError(t, err, "task daemon socket path not configured")
}

func TestStop_SendsStopRequestAndAcceptsStoppingResponse(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	requestCh := make(chan socketRequest, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()

		var req socketRequest
		if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
			errCh <- err
			return
		}
		requestCh <- req

		errCh <- json.NewEncoder(conn).Encode(socketEnvelope{
			Type: "stopping",
			OK:   true,
		})
	}()
	require.NoError(t, Stop(context.Background(), socketPath))

	require.Equal(t, socketRequest{Command: "stop"}, <-requestCh)
	require.NoError(t, <-errCh)
}

func TestConfigureDetachedProcess_SetsDetachedUnixProcessAttributes(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("/bin/sh", "-c", "sleep 1")
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	require.NoError(t, err)
	defer devNull.Close()

	configureDetachedProcess(cmd, devNull)

	require.NotNil(t, cmd.SysProcAttr)
	require.True(t, cmd.SysProcAttr.Setsid)
	require.Same(t, devNull, cmd.Stdin)
	require.Same(t, devNull, cmd.Stdout)
	require.Same(t, devNull, cmd.Stderr)
}
