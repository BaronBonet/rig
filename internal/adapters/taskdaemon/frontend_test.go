package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"reflect"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestFrontend_ImplementsTaskFrontend(t *testing.T) {
	t.Parallel()

	require.NotNil(t, New(Config{}).Frontend())
}

func TestFrontend_ListTasksSendsListTasksAndReturnsTasks(t *testing.T) {
	t.Parallel()

	socketPath := frontendTestSocketPath(t)
	expectedTasks := []*core.Task{
		{ID: "task-1", DisplayName: "First task"},
		{ID: "task-2", DisplayName: "Second task"},
	}
	requestCh := make(chan socketRequest, 1)
	serverErrCh := serveOneShotFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
		requestCh <- req
		return encoder.Encode(socketEnvelope{
			Type:  "tasks_list",
			OK:    true,
			Tasks: expectedTasks,
		})
	})

	frontend := New(Config{SocketPath: socketPath}).Frontend()

	tasks, err := frontend.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, socketRequest{Command: "list_tasks"}, <-requestCh)
	require.True(t, reflect.DeepEqual(expectedTasks, tasks))
	require.NoError(t, <-serverErrCh)
}

func TestFrontend_CreateTaskStreamLatestTaskStatusAndSubscribeTaskStatus(t *testing.T) {
	t.Parallel()

	t.Run("create task stream", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		input := core.CreateTaskInput{
			Cwd:      "/repo",
			Prompt:   "ship it",
			Provider: core.ProviderCodex,
		}
		expectedTask := &core.Task{ID: "task-123", DisplayName: "Ship it"}
		requestCh := make(chan socketRequest, 1)
		serverErrCh := serveStreamingFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
			requestCh <- req
			if err := encoder.Encode(socketEnvelope{
				Type: "task_create_progress",
				OK:   true,
				CreateProgress: &core.TaskCreateProgressEvent{
					Step: core.TaskCreateProgressSuggestingName,
				},
			}); err != nil {
				return err
			}
			return encoder.Encode(socketEnvelope{
				Type: "task_created",
				OK:   true,
				Task: expectedTask,
			})
		})

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		events, err := frontend.CreateTaskStream(t.Context(), input)
		require.NoError(t, err)
		require.Equal(t, socketRequest{Command: "create_task", Input: &input}, <-requestCh)

		select {
		case event, ok := <-events:
			require.True(t, ok)
			require.NotNil(t, event.Progress)
			require.Equal(t, core.TaskCreateProgressSuggestingName, event.Progress.Step)
			require.Nil(t, event.Task)
			require.NoError(t, event.Err)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for create progress event")
		}

		select {
		case event, ok := <-events:
			require.True(t, ok)
			require.Nil(t, event.Progress)
			require.Equal(t, expectedTask, event.Task)
			require.NoError(t, event.Err)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for create task result")
		}

		select {
		case _, ok := <-events:
			require.False(t, ok)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for create stream close")
		}

		require.NoError(t, <-serverErrCh)
	})

	t.Run("delete task", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		requestCh := make(chan socketRequest, 1)
		serverErrCh := serveOneShotFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
			requestCh <- req
			return encoder.Encode(socketEnvelope{
				Type: "task_deleted",
				OK:   true,
			})
		})

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		err := frontend.DeleteTask(t.Context(), "task-123")
		require.NoError(t, err)
		require.Equal(t, socketRequest{Command: "delete_task", TaskID: "task-123"}, <-requestCh)
		require.NoError(t, <-serverErrCh)
	})

	t.Run("open task session", func(t *testing.T) {
		t.Parallel()

		task := &core.Task{ID: "task-123", TmuxSession: "repo_task"}
		sessions := &stubTaskSessionClient{
			openTaskSessionFn: func(_ context.Context, got *core.Task) error {
				require.Same(t, task, got)
				return nil
			},
		}

		frontend := &frontend{sessions: sessions}

		err := frontend.OpenTaskSession(t.Context(), task)
		require.NoError(t, err)
	})

	t.Run("latest task status", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		expectedUpdate := &core.TaskStatusUpdate{
			TaskID:       "task-123",
			Provider:     core.ProviderCodex,
			Phase:        core.TaskStatusPhaseWorking,
			RawEventName: "turn.completed",
			ObservedAt:   time.Unix(1710000000, 0).UTC(),
		}
		requestCh := make(chan socketRequest, 1)
		serverErrCh := serveOneShotFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
			requestCh <- req
			return encoder.Encode(socketEnvelope{
				Type:   "task_status_snapshot",
				OK:     true,
				Update: expectedUpdate,
			})
		})

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		update, err := frontend.LatestTaskStatus(t.Context(), "task-123")
		require.NoError(t, err)
		require.Equal(t, socketRequest{Command: "latest_task_status", TaskID: "task-123"}, <-requestCh)
		require.Equal(t, expectedUpdate, update)
		require.NoError(t, <-serverErrCh)
	})

	t.Run("subscribe task status", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		expectedUpdate := core.TaskStatusUpdate{
			TaskID:       "task-123",
			Provider:     core.ProviderCodex,
			Phase:        core.TaskStatusPhaseWaitingForInput,
			RawEventName: "turn.waiting",
			ObservedAt:   time.Unix(1710000100, 0).UTC(),
		}
		requestCh := make(chan socketRequest, 1)
		serverErrCh := serveStreamingFrontendSocket(
			t,
			socketPath,
			func(req socketRequest, encoder *json.Encoder) error {
				requestCh <- req
				if err := encoder.Encode(socketEnvelope{Type: "subscribed", OK: true}); err != nil {
					return err
				}
				return encoder.Encode(socketEnvelope{Type: "task_status_update", Update: &expectedUpdate})
			},
		)

		frontend := New(Config{SocketPath: socketPath}).Frontend()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		updates, err := frontend.SubscribeTaskStatus(ctx, "task-123")
		require.NoError(t, err)
		require.Equal(t, socketRequest{Command: "subscribe_task_status", TaskID: "task-123"}, <-requestCh)

		select {
		case update, ok := <-updates:
			require.True(t, ok)
			require.Equal(t, expectedUpdate, update)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for status update")
		}

		cancel()

		require.NoError(t, <-serverErrCh)
	})

	t.Run("subscribe task status closes promptly on caller cancellation", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		requestCh := make(chan socketRequest, 1)
		serverReady := make(chan struct{})
		serverErrCh := serveStreamingFrontendSocketWithConn(
			t,
			socketPath,
			func(req socketRequest, encoder *json.Encoder, conn net.Conn) error {
				requestCh <- req
				if err := encoder.Encode(socketEnvelope{Type: "subscribed", OK: true}); err != nil {
					return err
				}
				close(serverReady)
				var buf [1]byte
				_, err := conn.Read(buf[:])
				return err
			},
		)

		frontend := New(Config{SocketPath: socketPath}).Frontend()
		ctx, cancel := context.WithCancel(context.Background())
		updates, err := frontend.SubscribeTaskStatus(ctx, "task-123")
		require.NoError(t, err)
		require.Equal(t, socketRequest{Command: "subscribe_task_status", TaskID: "task-123"}, <-requestCh)
		<-serverReady

		cancel()

		select {
		case _, ok := <-updates:
			require.False(t, ok)
		case <-time.After(250 * time.Millisecond):
			t.Fatal("timed out waiting for canceled subscription to close")
		}

		require.ErrorIs(t, <-serverErrCh, io.EOF)
	})

	t.Run("subscribe task status returns promptly when canceled before ack", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		requestCh := make(chan socketRequest, 1)
		serverReady := make(chan struct{})
		serverErrCh := serveStreamingFrontendSocketWithConn(
			t,
			socketPath,
			func(req socketRequest, encoder *json.Encoder, conn net.Conn) error {
				requestCh <- req
				close(serverReady)
				var buf [1]byte
				_, err := conn.Read(buf[:])
				return err
			},
		)

		frontend := New(Config{SocketPath: socketPath}).Frontend()
		ctx, cancel := context.WithCancel(context.Background())

		resultCh := make(chan error, 1)
		go func() {
			_, err := frontend.SubscribeTaskStatus(ctx, "task-123")
			resultCh <- err
		}()

		require.Equal(t, socketRequest{Command: "subscribe_task_status", TaskID: "task-123"}, <-requestCh)
		<-serverReady
		cancel()

		select {
		case err := <-resultCh:
			require.Error(t, err)
		case <-time.After(250 * time.Millisecond):
			t.Fatal("timed out waiting for canceled subscribe handshake to return")
		}

		require.ErrorIs(t, <-serverErrCh, io.EOF)
	})

	t.Run("create task stream yields terminal error", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		requestCh := make(chan socketRequest, 1)
		serverErrCh := serveStreamingFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
			requestCh <- req
			if err := encoder.Encode(socketEnvelope{
				Type: "task_create_progress",
				OK:   true,
				CreateProgress: &core.TaskCreateProgressEvent{
					Step: core.TaskCreateProgressCreatingWorktree,
				},
			}); err != nil {
				return err
			}
			return encoder.Encode(socketEnvelope{Type: "error", Error: "create failed"})
		})

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		events, err := frontend.CreateTaskStream(t.Context(), core.CreateTaskInput{Prompt: "ship it"})
		require.NoError(t, err)
		require.Equal(
			t,
			socketRequest{Command: "create_task", Input: &core.CreateTaskInput{Prompt: "ship it"}},
			<-requestCh,
		)

		select {
		case event, ok := <-events:
			require.True(t, ok)
			require.NotNil(t, event.Progress)
			require.Equal(t, core.TaskCreateProgressCreatingWorktree, event.Progress.Step)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for streamed create progress")
		}

		select {
		case event, ok := <-events:
			require.True(t, ok)
			require.Nil(t, event.Progress)
			require.Nil(t, event.Task)
			require.EqualError(t, event.Err, "create failed")
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for terminal create error")
		}

		select {
		case _, ok := <-events:
			require.False(t, ok)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for errored create stream close")
		}

		require.NoError(t, <-serverErrCh)
	})
}

type stubTaskSessionClient struct {
	openTaskSessionFn func(context.Context, *core.Task) error
}

func (s *stubTaskSessionClient) StartTaskSession(context.Context, *core.Task, core.TaskSessionLaunchSpec) error {
	panic("unexpected StartTaskSession call")
}

func (s *stubTaskSessionClient) OpenTaskSession(ctx context.Context, task *core.Task) error {
	if s.openTaskSessionFn != nil {
		return s.openTaskSessionFn(ctx, task)
	}
	return nil
}

func (s *stubTaskSessionClient) DeleteTaskSession(context.Context, *core.Task) error {
	panic("unexpected DeleteTaskSession call")
}

func (s *stubTaskSessionClient) InspectTaskSession(context.Context, *core.Task) (core.SessionResources, error) {
	panic("unexpected InspectTaskSession call")
}

func (s *stubTaskSessionClient) SnapshotTaskSession(context.Context, *core.Task) (core.RuntimeSnapshot, error) {
	panic("unexpected SnapshotTaskSession call")
}

func TestFrontend_ReturnsExplicitErrorsOnErrorEnvelopesAndUnexpectedTypes(t *testing.T) {
	t.Parallel()

	t.Run("list tasks returns error envelope", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		serverErrCh := serveOneShotFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
			return encoder.Encode(socketEnvelope{Type: "error", Error: "boom"})
		})

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		tasks, err := frontend.ListTasks(t.Context())
		require.Nil(t, tasks)
		require.EqualError(t, err, "boom")
		require.NoError(t, <-serverErrCh)
	})

	t.Run("create task stream returns unexpected response type", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		serverErrCh := serveStreamingFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
			return encoder.Encode(socketEnvelope{Type: "tasks_list", OK: true})
		})

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		events, err := frontend.CreateTaskStream(t.Context(), core.CreateTaskInput{Prompt: "hello"})
		require.NoError(t, err)

		select {
		case event, ok := <-events:
			require.True(t, ok)
			require.Nil(t, event.Task)
			require.NotNil(t, event.Err)
			require.EqualError(t, event.Err, `unexpected create_task response "tasks_list"`)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for create stream error")
		}

		select {
		case _, ok := <-events:
			require.False(t, ok)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for create stream close")
		}
		require.NoError(t, <-serverErrCh)
	})

	t.Run("subscribe task status returns error envelope", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		serverErrCh := serveStreamingFrontendSocket(
			t,
			socketPath,
			func(req socketRequest, encoder *json.Encoder) error {
				return encoder.Encode(socketEnvelope{Type: "error", Error: "subscribe failed"})
			},
		)

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		updates, err := frontend.SubscribeTaskStatus(t.Context(), "task-123")
		require.Nil(t, updates)
		require.EqualError(t, err, "subscribe failed")
		require.NoError(t, <-serverErrCh)
	})

	t.Run("subscribe task status closes stream on malformed update frame", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		requestCh := make(chan socketRequest, 1)
		serverErrCh := serveStreamingFrontendSocket(
			t,
			socketPath,
			func(req socketRequest, encoder *json.Encoder) error {
				requestCh <- req
				if err := encoder.Encode(socketEnvelope{Type: "subscribed", OK: true}); err != nil {
					return err
				}
				return encoder.Encode(socketEnvelope{Type: "tasks_list", OK: true})
			},
		)

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		updates, err := frontend.SubscribeTaskStatus(t.Context(), "task-123")
		require.NoError(t, err)
		require.Equal(t, socketRequest{Command: "subscribe_task_status", TaskID: "task-123"}, <-requestCh)

		select {
		case _, ok := <-updates:
			require.False(t, ok)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for malformed stream to close")
		}

		require.NoError(t, <-serverErrCh)
	})

	t.Run("latest task status returns unexpected response type", func(t *testing.T) {
		t.Parallel()

		socketPath := frontendTestSocketPath(t)
		serverErrCh := serveOneShotFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
			return encoder.Encode(socketEnvelope{Type: "task_created", OK: true})
		})

		frontend := New(Config{SocketPath: socketPath}).Frontend()

		update, err := frontend.LatestTaskStatus(t.Context(), "task-123")
		require.Nil(t, update)
		require.EqualError(t, err, `unexpected latest_task_status response "task_created"`)
		require.NoError(t, <-serverErrCh)
	})
}

func frontendTestSocketPath(t *testing.T) string {
	t.Helper()

	file, err := os.CreateTemp(os.TempDir(), "tdf-*.sock")
	require.NoError(t, err)
	path := file.Name()
	require.NoError(t, file.Close())
	require.NoError(t, os.Remove(path))
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	return path
}

func serveOneShotFrontendSocket(
	t *testing.T,
	socketPath string,
	handler func(req socketRequest, encoder *json.Encoder) error,
) <-chan error {
	t.Helper()

	listener, err := listenUnixSocket(t.Context(), socketPath)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		defer listener.Close()

		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		defer conn.Close()

		var req socketRequest
		if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
			errCh <- err
			return
		}

		errCh <- handler(req, json.NewEncoder(conn))
	}()

	return errCh
}

func serveStreamingFrontendSocket(
	t *testing.T,
	socketPath string,
	handler func(req socketRequest, encoder *json.Encoder) error,
) <-chan error {
	t.Helper()

	return serveStreamingFrontendSocketWithConn(
		t,
		socketPath,
		func(req socketRequest, encoder *json.Encoder, _ net.Conn) error {
			return handler(req, encoder)
		},
	)
}

func serveStreamingFrontendSocketWithConn(
	t *testing.T,
	socketPath string,
	handler func(req socketRequest, encoder *json.Encoder, conn net.Conn) error,
) <-chan error {
	t.Helper()

	listener, err := listenUnixSocket(t.Context(), socketPath)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		defer listener.Close()

		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		defer conn.Close()

		var req socketRequest
		if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
			errCh <- err
			return
		}

		errCh <- handler(req, json.NewEncoder(conn), conn)
	}()

	return errCh
}
