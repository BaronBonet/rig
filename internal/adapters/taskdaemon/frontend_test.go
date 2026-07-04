package taskdaemon

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// These tests cover the client's handling of misbehaving or interrupted
// daemons — responses a correct server never produces. The happy paths are
// covered end to end by the round-trip suite in server_test.go.

func TestFrontend_ImplementsTaskFrontend(t *testing.T) {
	t.Parallel()

	var _ core.TaskFrontend = &frontend{}
}

// fakeDaemonSocket accepts connections and answers each request with the
// scripted raw JSON frames, then closes the connection.
func fakeDaemonSocket(t *testing.T, frames ...string) string {
	t.Helper()

	socketPath := serverTestSocketPath(t)
	listener, err := listenUnixSocket(context.Background(), socketPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
	})

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var req socketRequest
				if decodeErr := json.NewDecoder(conn).Decode(&req); decodeErr != nil {
					return
				}
				for _, frame := range frames {
					if _, writeErr := conn.Write([]byte(frame + "\n")); writeErr != nil {
						return
					}
				}
			}(conn)
		}
	}()

	return socketPath
}

func TestFrontend_RejectsUnexpectedResponseType(t *testing.T) {
	t.Parallel()

	client := &frontend{socketPath: fakeDaemonSocket(t, `{"type":"not_tasks_list","ok":true}`)}

	_, err := client.ListTasks(context.Background())
	require.ErrorContains(t, err, `unexpected list_tasks response "not_tasks_list"`)
}

func TestFrontend_SurfacesErrorEnvelope(t *testing.T) {
	t.Parallel()

	client := &frontend{socketPath: fakeDaemonSocket(t, `{"type":"error","error":"task repository offline"}`)}

	_, err := client.ListTasks(context.Background())
	require.ErrorContains(t, err, "task repository offline")
}

func TestFrontend_CreateTaskStreamYieldsErrorWhenSocketClosesBeforeTerminalResult(t *testing.T) {
	t.Parallel()

	client := &frontend{socketPath: fakeDaemonSocket(t,
		`{"type":"task_create_progress","ok":true,"payload":{"step":"suggesting_name"}}`,
	)}

	events, err := client.CreateTaskStream(context.Background(), core.CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add retries",
		Provider: core.ProviderCodex,
	})
	require.NoError(t, err)

	var got []core.TaskCreateEvent
	for event := range events {
		got = append(got, event)
	}
	require.Len(t, got, 2)
	require.Equal(t, core.TaskCreateProgressSuggestingName, got[0].Progress.Step)
	require.ErrorContains(t, got[1].Err, "create task stream closed before terminal result")
}

func TestFrontend_CreateTaskStreamRejectsUnexpectedFrame(t *testing.T) {
	t.Parallel()

	client := &frontend{socketPath: fakeDaemonSocket(t, `{"type":"tasks_list","ok":true}`)}

	events, err := client.CreateTaskStream(context.Background(), core.CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add retries",
		Provider: core.ProviderCodex,
	})
	require.NoError(t, err)

	var got []core.TaskCreateEvent
	for event := range events {
		got = append(got, event)
	}
	require.Len(t, got, 1)
	require.ErrorContains(t, got[0].Err, `unexpected task create stream message "tasks_list"`)
}

func TestFrontend_SubscribeTaskStatusRejectsBadAck(t *testing.T) {
	t.Parallel()

	t.Run("error envelope ack", func(t *testing.T) {
		client := &frontend{socketPath: fakeDaemonSocket(t, `{"type":"error","error":"subscribe failed"}`)}

		_, err := client.SubscribeTaskStatus(context.Background(), "task-1")
		require.ErrorContains(t, err, "subscribe failed")
	})

	t.Run("unexpected ack type", func(t *testing.T) {
		client := &frontend{socketPath: fakeDaemonSocket(t, `{"type":"tasks_list","ok":true}`)}

		_, err := client.SubscribeTaskStatus(context.Background(), "task-1")
		require.ErrorContains(t, err, `unexpected subscribe_task_status response "tasks_list"`)
	})
}

func TestFrontend_SubscribeTaskStatusClosesStreamOnMalformedUpdateFrame(t *testing.T) {
	t.Parallel()

	client := &frontend{socketPath: fakeDaemonSocket(t,
		`{"type":"subscribed","ok":true}`,
		`{"type":"task_status_update","ok":true}`,
	)}

	updates, err := client.SubscribeTaskStatus(context.Background(), "task-1")
	require.NoError(t, err)

	select {
	case _, open := <-updates:
		require.False(t, open, "malformed frame must close the stream, not deliver an update")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream close")
	}
}

func TestFrontend_SubscribeTaskStatusReturnsPromptlyWhenCanceledBeforeAck(t *testing.T) {
	t.Parallel()

	// A daemon that accepts the request and then never answers.
	socketPath := serverTestSocketPath(t)
	listener, err := listenUnixSocket(context.Background(), socketPath)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
	})
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				var req socketRequest
				_ = json.NewDecoder(conn).Decode(&req)
				// Hold the connection open without responding.
			}(conn)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, subscribeErr := (&frontend{socketPath: socketPath}).SubscribeTaskStatus(ctx, "task-1")
		done <- subscribeErr
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case subscribeErr := <-done:
		require.Error(t, subscribeErr)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled subscribe to return")
	}
}

func TestFrontend_AttachTaskSessionRequiresSessionClient(t *testing.T) {
	t.Parallel()

	client := &frontend{}
	require.ErrorContains(t,
		client.AttachTaskSession(context.Background(), &core.Task{ID: "task-1"}),
		"task session client not configured",
	)
}
