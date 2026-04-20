package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskDaemonProcessConfig_OnlyExposesRuntimeWiringFields(t *testing.T) {
	t.Parallel()

	typ := reflect.TypeOf(taskDaemonProcessConfig{})
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

func TestStopTaskDaemon_RequiresSocketPath(t *testing.T) {
	t.Parallel()

	err := stopTaskDaemon(context.Background(), "")
	require.EqualError(t, err, "task daemon socket path not configured")
}

func TestStopTaskDaemon_SendsStopRequestAndAcceptsStoppingResponse(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	requestCh := make(chan daemonSocketRequest, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()

		var req daemonSocketRequest
		if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
			errCh <- err
			return
		}
		requestCh <- req

		errCh <- json.NewEncoder(conn).Encode(daemonSocketEnvelope{
			Type: "stopping",
			OK:   true,
		})
	}()
	require.NoError(t, stopTaskDaemon(context.Background(), socketPath))

	require.Equal(t, daemonSocketRequest{Command: "stop"}, <-requestCh)
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

func testSocketPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(os.TempDir(), "rig-debug-taskdaemon-"+time.Now().UTC().Format("20060102150405.000000000")+".sock")
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	return path
}
