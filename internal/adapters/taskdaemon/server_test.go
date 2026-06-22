package taskdaemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestServer_ImplementsSocketBackend(t *testing.T) {
	var _ socketBackend = &server{}
}

func TestSocketUnaryCommandsRegisterUnaryCommands(t *testing.T) {
	t.Parallel()

	require.Contains(t, socketUnaryCommands, socketCommandHealth)
	require.Contains(t, socketUnaryCommands, socketCommandProtocolVersion)
	require.Contains(t, socketUnaryCommands, socketCommandFrontendBuildVersion)
	require.Contains(t, socketUnaryCommands, socketCommandDeleteTask)
	require.Contains(t, socketUnaryCommands, socketCommandReconnectTaskSession)
	require.Contains(t, socketUnaryCommands, socketCommandGetTaskActivity)
	require.Contains(t, socketUnaryCommands, socketCommandGetTaskTokenUsage)
	require.Contains(t, socketUnaryCommands, socketCommandLatestTaskStatus)
	require.Contains(t, socketUnaryCommands, socketCommandListRepoPullRequests)
	require.Contains(t, socketUnaryCommands, socketCommandListTasks)
	require.Contains(t, socketUnaryCommands, socketCommandPullRequestStatus)

	require.NotContains(t, socketUnaryCommands, socketCommandCreateTask)
	require.NotContains(t, socketUnaryCommands, socketCommandRetryTaskCreation)
	require.NotContains(t, socketUnaryCommands, socketCommandSubscribeTaskStatus)
	require.NotContains(t, socketUnaryCommands, socketCommandStop)
}

func TestUnixSocketServer_SecuresSocketDirectoryAndSocketPermissions(t *testing.T) {
	t.Parallel()

	socketDir := serverTestSocketDir(t)
	require.NoError(t, os.Chmod(socketDir, 0o755))
	socketPath := filepath.Join(socketDir, "daemon.sock")

	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, core.NewMockTaskService(t), nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	socketDirInfo, err := os.Stat(socketDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), socketDirInfo.Mode().Perm())

	socketInfo, err := os.Stat(socketPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), socketInfo.Mode().Perm())

	cancel()
	require.NoError(t, <-errCh)
}

func TestAuthorizeUnixSocketPeerUIDAllowsOnlyMatchingUID(t *testing.T) {
	t.Parallel()

	require.NoError(t, authorizeUnixSocketPeerUID(501, 501))
	require.Error(t, authorizeUnixSocketPeerUID(502, 501))
}

func TestUnixSocketServer_CreateTaskCallsTaskService(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().CreateTaskWithProgress(
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).RunAndReturn(func(
		_ context.Context,
		input core.CreateTaskInput,
		_ core.TaskCreateProgressReporter,
	) (*core.Task, error) {
		require.Equal(t, core.CreateTaskInput{
			Cwd:      "/tmp/repo",
			Prompt:   "add retries",
			Provider: core.ProviderCodex,
		}, input)
		return &core.Task{
			ID:          "task-1",
			DisplayName: "add retries",
		}, nil
	})
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	task, err := createTaskViaSocket(context.Background(), socketPath, core.CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add retries",
		Provider: core.ProviderCodex,
	})
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, "task-1", task.ID)
	require.Equal(t, "add retries", task.DisplayName)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_CreateTaskStreamsProgressBeforeTerminalResult(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().CreateTaskWithProgress(
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).RunAndReturn(func(
		ctx context.Context,
		input core.CreateTaskInput,
		reporter core.TaskCreateProgressReporter,
	) (*core.Task, error) {
		require.Equal(t, "add retries", input.Prompt)
		require.NotNil(t, reporter)
		reporter.ReportTaskCreateProgress(core.TaskCreateProgressSuggestingName)
		reporter.ReportTaskCreateProgress(core.TaskCreateProgressCreatingWorktree)
		return &core.Task{
			ID:          "task-1",
			DisplayName: "add retries",
		}, nil
	})
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	events, err := createTaskStreamViaSocket(context.Background(), socketPath, core.CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add retries",
		Provider: core.ProviderCodex,
	})
	require.NoError(t, err)
	require.Len(t, events, 3)
	require.NotNil(t, events[0].CreateProgress)
	require.Equal(t, core.TaskCreateProgressSuggestingName, events[0].CreateProgress.Step)
	require.NotNil(t, events[1].CreateProgress)
	require.Equal(t, core.TaskCreateProgressCreatingWorktree, events[1].CreateProgress.Step)
	require.NotNil(t, events[2].Task)
	require.Equal(t, "task-1", events[2].Task.ID)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_CreateTaskStreamsTerminalErrorOnFailure(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().CreateTaskWithProgress(
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).RunAndReturn(func(
		ctx context.Context,
		input core.CreateTaskInput,
		reporter core.TaskCreateProgressReporter,
	) (*core.Task, error) {
		require.Equal(t, "add retries", input.Prompt)
		require.NotNil(t, reporter)
		reporter.ReportTaskCreateProgress(core.TaskCreateProgressPreparingWorkspace)
		return &core.Task{
			ID:             "task-1",
			DisplayName:    "add retries",
			CreationStatus: core.TaskCreationStatusFailed,
			CreationStep:   core.TaskCreateProgressPreparingWorkspace,
			CreationError:  "create failed",
		}, assertiveError("create failed")
	})
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	events, err := createTaskStreamViaSocket(context.Background(), socketPath, core.CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add retries",
		Provider: core.ProviderCodex,
	})
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.NotNil(t, events[0].CreateProgress)
	require.Equal(t, core.TaskCreateProgressPreparingWorkspace, events[0].CreateProgress.Step)
	require.Equal(t, "error", events[1].Type)
	require.Equal(t, "create failed", events[1].Error)
	require.NotNil(t, events[1].Task)
	require.Equal(t, "task-1", events[1].Task.ID)
	require.Equal(t, core.TaskCreationStatusFailed, events[1].Task.CreationStatus)
	require.Equal(t, core.TaskCreateProgressPreparingWorkspace, events[1].Task.CreationStep)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_RetryTaskCreationStreamsProgressBeforeTerminalResult(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().RetryTaskCreationWithProgress(
		mock.Anything,
		"task-1",
		mock.Anything,
	).RunAndReturn(func(
		_ context.Context,
		taskID string,
		reporter core.TaskCreateProgressReporter,
	) (*core.Task, error) {
		require.Equal(t, "task-1", taskID)
		require.NotNil(t, reporter)
		reporter.ReportTaskCreateProgress(core.TaskCreateProgressPreparingWorkspace)
		reporter.ReportTaskCreateProgress(core.TaskCreateProgressStartingSession)
		return &core.Task{
			ID:             "task-1",
			DisplayName:    "add retries",
			CreationStatus: core.TaskCreationStatusReady,
		}, nil
	})
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	events, err := taskCreateStreamViaSocket(context.Background(), socketPath, socketRequest{
		Command: socketCommandRetryTaskCreation,
		TaskID:  "task-1",
	})
	require.NoError(t, err)
	require.Len(t, events, 3)
	require.NotNil(t, events[0].CreateProgress)
	require.Equal(t, core.TaskCreateProgressPreparingWorkspace, events[0].CreateProgress.Step)
	require.NotNil(t, events[1].CreateProgress)
	require.Equal(t, core.TaskCreateProgressStartingSession, events[1].CreateProgress.Step)
	require.NotNil(t, events[2].Task)
	require.Equal(t, core.TaskCreationStatusReady, events[2].Task.CreationStatus)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_ListTasksReturnsTasksSnapshot(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().ListTasks(mock.Anything).RunAndReturn(
		func(context.Context) ([]*core.Task, error) {
			return []*core.Task{
				{ID: "task-1", Slug: "repo-a-task", RepoName: "repo-a"},
				{ID: "task-2", Slug: "repo-b-task", RepoName: "repo-b"},
			}, nil
		},
	)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	tasks, err := listTasksViaSocket(context.Background(), socketPath)
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, []string{"task-1", "task-2"}, []string{tasks[0].ID, tasks[1].ID})

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_ListRepoPullRequestsReturnsRepoScopedSnapshot(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().ListRepoPullRequests(mock.Anything, "/tmp/repo").RunAndReturn(
		func(_ context.Context, cwd string) ([]core.RepoPullRequest, error) {
			require.Equal(t, "/tmp/repo", cwd)
			return []core.RepoPullRequest{
				{Number: 41, Title: "Billing", BranchName: "feat/billing", State: core.PRStateOpen},
				{Number: 42, Title: "Auth", BranchName: "feat/auth", State: core.PRStateDraft, HasExistingTask: true},
			}, nil
		},
	)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	prs, err := listRepoPullRequestsViaSocket(context.Background(), socketPath, "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, []core.RepoPullRequest{
		{Number: 41, Title: "Billing", BranchName: "feat/billing", State: core.PRStateOpen},
		{Number: 42, Title: "Auth", BranchName: "feat/auth", State: core.PRStateDraft, HasExistingTask: true},
	}, prs)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_PullRequestStatusReturnsRepoBranchStatus(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().PullRequestStatus(mock.Anything, "/tmp/repo", "feat/auth").RunAndReturn(
		func(_ context.Context, repoRoot string, branchName string) (*core.PRStatus, error) {
			require.Equal(t, "/tmp/repo", repoRoot)
			require.Equal(t, "feat/auth", branchName)
			return &core.PRStatus{State: core.PRStateOpen, Number: 42}, nil
		},
	)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	status, err := pullRequestStatusViaSocket(context.Background(), socketPath, "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, &core.PRStatus{State: core.PRStateOpen, Number: 42}, status)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_DeleteTaskCallsTaskService(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	deletedTaskIDs := make(chan string, 1)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().DeleteTask(mock.Anything, "task-1").RunAndReturn(
		func(_ context.Context, taskID string) error {
			deletedTaskIDs <- taskID
			return nil
		},
	)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	err := deleteTaskViaSocket(context.Background(), socketPath, "task-1")
	require.NoError(t, err)
	require.Equal(t, "task-1", <-deletedTaskIDs)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_ReconnectTaskSessionCallsTaskService(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	reconnectedTaskIDs := make(chan string, 1)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().ReconnectTaskSession(mock.Anything, "task-1").RunAndReturn(
		func(_ context.Context, taskID string) error {
			reconnectedTaskIDs <- taskID
			return nil
		},
	)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	err := reconnectTaskSessionViaSocket(context.Background(), socketPath, "task-1")
	require.NoError(t, err)
	require.Equal(t, "task-1", <-reconnectedTaskIDs)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_GetTaskActivityCallsTaskService(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	expectedEvents := []core.TaskActivityEvent{
		{
			TaskID:     "task-1",
			TurnID:     "turn-1",
			EventName:  "UserPromptSubmit",
			Role:       core.TaskActivityRoleUser,
			Text:       "restore preview",
			ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
		},
		{
			TaskID:     "task-1",
			TurnID:     "turn-1",
			EventName:  "Stop",
			Role:       core.TaskActivityRoleAssistant,
			Text:       "Restored preview",
			ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
		},
	}
	svc := core.NewMockTaskService(t)
	svc.EXPECT().GetTaskActivity(mock.Anything, "task-1", 5).Return(expectedEvents, nil)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	events, err := getTaskActivityViaSocket(context.Background(), socketPath, "task-1", 5)
	require.NoError(t, err)
	require.Equal(t, expectedEvents, events)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_GetTaskTokenUsageCallsTaskService(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	expectedUsage := &core.TaskTokenUsage{
		SessionCount: 2,
		InputTokens:  130,
		OutputTokens: 60,
		TotalTokens:  190,
	}
	svc := core.NewMockTaskService(t)
	svc.EXPECT().GetTaskTokenUsage(mock.Anything, "task-1").Return(expectedUsage, nil)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	usage, err := getTaskTokenUsageViaSocket(context.Background(), socketPath, "task-1")
	require.NoError(t, err)
	require.Equal(t, expectedUsage, usage)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_SubscribeTaskStatusStreamsMatchingUpdates(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	updates := make(chan core.TaskStatusUpdate, 1)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().SubscribeTaskStatus(mock.Anything, "task-1").RunAndReturn(
		func(_ context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
			require.Equal(t, "task-1", taskID)
			return updates, nil
		},
	)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	stream, cleanup, err := subscribeTaskStatusViaSocket(streamCtx, socketPath, "task-1")
	require.NoError(t, err)
	defer cleanup()

	expected := core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   time.Now().UTC(),
	}
	updates <- expected

	select {
	case got := <-stream:
		require.Equal(t, expected, got)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed task status update")
	}

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_SubscribeTaskStatusReturnsErrorEnvelopeWhenSubscribeFails(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().SubscribeTaskStatus(mock.Anything, "task-1").RunAndReturn(
		func(_ context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
			require.Equal(t, "task-1", taskID)
			return nil, assertiveError("subscribe failed")
		},
	)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	resp, err := subscribeTaskStatusHandshake(context.Background(), socketPath, "task-1")
	require.NoError(t, err)
	require.Equal(t, "error", resp.Type)
	require.Equal(t, "subscribe failed", resp.Error)
	require.False(t, resp.OK)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_SubscribeTaskStatusCancelsBackendContextOnClientDisconnect(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	subscribeCtxDone := make(chan struct{})
	svc := core.NewMockTaskService(t)
	svc.EXPECT().SubscribeTaskStatus(mock.Anything, "task-1").RunAndReturn(
		func(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
			require.Equal(t, "task-1", taskID)
			go func() {
				<-ctx.Done()
				close(subscribeCtxDone)
			}()
			return make(chan core.TaskStatusUpdate), nil
		},
	)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	_, cleanup, err := subscribeTaskStatusViaSocket(context.Background(), socketPath, "task-1")
	require.NoError(t, err)
	cleanup()

	select {
	case <-subscribeCtxDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for backend subscribe context cancellation")
	}

	cancel()
	require.NoError(t, <-errCh)
}

func TestHTTPHookServer_DelegatesToInjectedHookHandler(t *testing.T) {
	t.Parallel()

	called := false
	server := newHTTPHookServer([]core.TaskDaemonHookRoute{{
		Path: "/hook",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusAccepted)
		}),
	}})

	req := httptestNewRequest(t, http.MethodPost, "/hook", map[string]any{"ok": true})

	rec := newRecorder()
	server.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.True(t, called)
}

func TestListenForHTTPHooksRejectsNonLoopbackAddress(t *testing.T) {
	t.Parallel()

	listener, err := listenForHTTPHooks(t.Context(), "0.0.0.0:0")
	if listener != nil {
		require.NoError(t, listener.Close())
	}

	require.Error(t, err)
	require.Contains(t, err.Error(), "unsafe")
}

func waitForUnixSocketServer(t *testing.T, socketPath string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := probeSocketHealth(context.Background(), socketPath); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("unix socket server at %s did not become healthy", socketPath)
}

func serverTestSocketPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(serverTestSocketDir(t), "daemon.sock")
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	return path
}

func serverTestSocketDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp(os.TempDir(), "rig-td-")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	return dir
}

func createTaskViaSocket(ctx context.Context, socketPath string, input core.CreateTaskInput) (*core.Task, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "create_task",
		Input:   &input,
	}); err != nil {
		return nil, err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type != "task_created" || resp.Task == nil {
		return nil, nil
	}

	return resp.Task, nil
}

func createTaskStreamViaSocket(
	ctx context.Context,
	socketPath string,
	input core.CreateTaskInput,
) ([]socketEnvelope, error) {
	return taskCreateStreamViaSocket(ctx, socketPath, socketRequest{
		Command: "create_task",
		Input:   &input,
	})
}

func taskCreateStreamViaSocket(
	ctx context.Context,
	socketPath string,
	request socketRequest,
) ([]socketEnvelope, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(conn)
	var events []socketEnvelope
	for {
		var resp socketEnvelope
		if err := decoder.Decode(&resp); err != nil {
			break
		}
		events = append(events, resp)
		if resp.Type == "task_created" || resp.Type == "error" {
			break
		}
	}

	return events, nil
}

func deleteTaskViaSocket(ctx context.Context, socketPath string, taskID string) error {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "delete_task",
		TaskID:  taskID,
	}); err != nil {
		return err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if resp.Type == "error" && resp.Error != "" {
		return assertiveError(resp.Error)
	}
	if resp.Type != "task_deleted" || !resp.OK {
		return assertiveError("unexpected delete response")
	}

	return nil
}

func reconnectTaskSessionViaSocket(ctx context.Context, socketPath string, taskID string) error {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "reconnect_task_session",
		TaskID:  taskID,
	}); err != nil {
		return err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if resp.Type == "error" && resp.Error != "" {
		return assertiveError(resp.Error)
	}
	if resp.Type != "task_session_reconnected" || !resp.OK {
		return assertiveError("unexpected reconnect response")
	}

	return nil
}

func getTaskActivityViaSocket(
	ctx context.Context,
	socketPath string,
	taskID string,
	limit int,
) ([]core.TaskActivityEvent, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "get_task_activity",
		TaskID:  taskID,
		Limit:   limit,
	}); err != nil {
		return nil, err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type == "error" && resp.Error != "" {
		return nil, assertiveError(resp.Error)
	}
	if resp.Type != "task_activity" || !resp.OK {
		return nil, assertiveError("unexpected task activity response")
	}

	return resp.Activity, nil
}

func getTaskTokenUsageViaSocket(
	ctx context.Context,
	socketPath string,
	taskID string,
) (*core.TaskTokenUsage, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "get_task_token_usage",
		TaskID:  taskID,
	}); err != nil {
		return nil, err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type == "error" && resp.Error != "" {
		return nil, assertiveError(resp.Error)
	}
	if resp.Type != "task_token_usage" || !resp.OK {
		return nil, assertiveError("unexpected task token usage response")
	}

	return resp.Usage, nil
}

func listTasksViaSocket(ctx context.Context, socketPath string) ([]*core.Task, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "list_tasks",
	}); err != nil {
		return nil, err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type != "tasks_list" {
		return nil, nil
	}

	return resp.Tasks, nil
}

func listRepoPullRequestsViaSocket(ctx context.Context, socketPath string, cwd string) ([]core.RepoPullRequest, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "list_repo_pull_requests",
		Cwd:     cwd,
	}); err != nil {
		return nil, err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type != "repo_pull_requests_list" {
		return nil, nil
	}

	return resp.PullRequests, nil
}

func pullRequestStatusViaSocket(
	ctx context.Context,
	socketPath string,
	repoRoot string,
	branchName string,
) (*core.PRStatus, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command:    "pull_request_status",
		Cwd:        repoRoot,
		BranchName: branchName,
	}); err != nil {
		return nil, err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type != "pull_request_status" {
		return nil, nil
	}

	return resp.PR, nil
}

func subscribeTaskStatusViaSocket(
	ctx context.Context,
	socketPath string,
	taskID string,
) (<-chan core.TaskStatusUpdate, func(), error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, nil, err
	}

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "subscribe_task_status",
		TaskID:  taskID,
	}); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	var ack socketEnvelope
	if err := json.NewDecoder(conn).Decode(&ack); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if ack.Type != "subscribed" {
		_ = conn.Close()
		return nil, nil, nil
	}

	updates := make(chan core.TaskStatusUpdate, 1)
	go func() {
		defer close(updates)
		defer conn.Close()

		decoder := json.NewDecoder(conn)
		for {
			var msg socketEnvelope
			if err := decoder.Decode(&msg); err != nil {
				return
			}
			if msg.Type == "task_status_update" && msg.Update != nil {
				updates <- *msg.Update
			}
		}
	}()

	return updates, func() { _ = conn.Close() }, nil
}

func subscribeTaskStatusHandshake(ctx context.Context, socketPath string, taskID string) (*socketEnvelope, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "subscribe_task_status",
		TaskID:  taskID,
	}); err != nil {
		return nil, err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

type assertiveError string

func (e assertiveError) Error() string {
	return string(e)
}

type recorder struct {
	HeaderMap http.Header
	Body      bytes.Buffer
	Code      int
}

func newRecorder() *recorder {
	return &recorder{HeaderMap: make(http.Header)}
}

func (r *recorder) Header() http.Header {
	return r.HeaderMap
}

func (r *recorder) Write(data []byte) (int, error) {
	if r.Code == 0 {
		r.Code = http.StatusOK
	}
	return r.Body.Write(data)
}

func (r *recorder) WriteHeader(statusCode int) {
	r.Code = statusCode
}

func httptestNewRequest(t *testing.T, method string, target string, payload map[string]any) *http.Request {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), method, target, bytes.NewReader(body))
	require.NoError(t, err)
	return req
}
