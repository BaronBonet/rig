package taskdaemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// fakeTaskService is a stateful in-memory core.TaskService used to exercise
// the socket transport end to end: real frontend client, real wire, real
// dispatcher, fake behaviour behind the port.
type fakeTaskService struct {
	mu sync.Mutex

	tasks      []*core.Task
	activity   map[string][]core.TaskActivityEvent
	usage      map[string]*core.TaskTokenUsage
	prs        map[string][]core.RepoPullRequest
	prStatuses map[string]*core.PRStatus
	setup      *core.ProviderSetup
	detections []core.ProviderDetection
	latest     map[string]*core.TaskStatusUpdate

	createSteps []core.TaskCreateProgressStep
	createTask  *core.Task
	createErr   error

	updates      chan core.TaskStatusUpdate
	subscribeErr error
	subscribeCtx chan context.Context

	deleted     []string
	reconnected []string

	errByOp map[string]error
}

func newFakeTaskService() *fakeTaskService {
	return &fakeTaskService{
		activity:     map[string][]core.TaskActivityEvent{},
		usage:        map[string]*core.TaskTokenUsage{},
		prs:          map[string][]core.RepoPullRequest{},
		prStatuses:   map[string]*core.PRStatus{},
		latest:       map[string]*core.TaskStatusUpdate{},
		updates:      make(chan core.TaskStatusUpdate, 8),
		subscribeCtx: make(chan context.Context, 1),
		errByOp:      map[string]error{},
	}
}

func (f *fakeTaskService) taskCreateResult() (<-chan core.TaskCreateEvent, error) {
	f.mu.Lock()
	steps, task, err := f.createSteps, f.createTask, f.createErr
	f.mu.Unlock()

	events := make(chan core.TaskCreateEvent, len(steps)+1)
	for _, step := range steps {
		events <- core.TaskCreateEvent{Progress: &core.TaskCreateProgressEvent{Step: step}}
	}
	if err != nil {
		events <- core.TaskCreateEvent{Err: err, Task: task}
	} else {
		events <- core.TaskCreateEvent{Task: task}
	}
	close(events)

	return events, nil
}

func (f *fakeTaskService) CreateTaskStream(
	_ context.Context,
	input core.CreateTaskInput,
) (<-chan core.TaskCreateEvent, error) {
	f.mu.Lock()
	f.tasks = append(f.tasks, f.createTask)
	f.mu.Unlock()

	return f.taskCreateResult()
}

func (f *fakeTaskService) RetryTaskCreationStream(
	_ context.Context,
	taskID string,
) (<-chan core.TaskCreateEvent, error) {
	return f.taskCreateResult()
}

func (f *fakeTaskService) ListRepoPullRequests(_ context.Context, cwd string) ([]core.RepoPullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prs[cwd], f.errByOp["list_repo_pull_requests"]
}

func (f *fakeTaskService) PullRequestStatus(
	_ context.Context,
	repoRoot string,
	branchName string,
) (*core.PRStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prStatuses[repoRoot+"|"+branchName], f.errByOp["pull_request_status"]
}

func (f *fakeTaskService) GetTaskActivity(
	_ context.Context,
	taskID string,
	limit int,
) ([]core.TaskActivityEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	events := f.activity[taskID]
	if limit > 0 && limit < len(events) {
		events = events[len(events)-limit:]
	}
	return events, f.errByOp["get_task_activity"]
}

func (f *fakeTaskService) GetTaskTokenUsage(_ context.Context, taskID string) (*core.TaskTokenUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.usage[taskID], f.errByOp["get_task_token_usage"]
}

func (f *fakeTaskService) ListTasks(context.Context) ([]*core.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tasks, f.errByOp["list_tasks"]
}

func (f *fakeTaskService) LatestTaskStatus(_ context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latest[taskID], f.errByOp["latest_task_status"]
}

func (f *fakeTaskService) SubscribeTaskStatus(
	ctx context.Context,
	taskID string,
) (<-chan core.TaskStatusUpdate, error) {
	f.mu.Lock()
	err := f.subscribeErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case f.subscribeCtx <- ctx:
	default:
	}

	return f.updates, nil
}

func (f *fakeTaskService) DeleteTask(_ context.Context, taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, taskID)
	return f.errByOp["delete_task"]
}

func (f *fakeTaskService) ReconnectTaskSession(_ context.Context, taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconnected = append(f.reconnected, taskID)
	return f.errByOp["reconnect_task_session"]
}

func (f *fakeTaskService) GetProviderSetup(context.Context) (*core.ProviderSetup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setup, f.errByOp["get_provider_setup"]
}

func (f *fakeTaskService) SaveProviderSetup(_ context.Context, setup core.ProviderSetup) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errByOp["save_provider_setup"]; err != nil {
		return err
	}
	f.setup = &setup
	return nil
}

func (f *fakeTaskService) DetectProviders(context.Context) ([]core.ProviderDetection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.detections, f.errByOp["detect_providers"]
}

func (f *fakeTaskService) SwitchTaskProvider(
	_ context.Context,
	taskID string,
	provider core.Provider,
) (*core.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errByOp["switch_task_provider"]; err != nil {
		return nil, err
	}
	for _, task := range f.tasks {
		if task != nil && task.ID == taskID {
			task.Provider = provider
			return task, nil
		}
	}
	return nil, errors.New("task not found: " + taskID)
}

// startTestFrontend serves a fake TaskService on a real Unix socket and
// returns the real daemon-backed frontend client pointed at it, so tests
// cross the seam exactly the way the TUI does.
func startTestFrontend(t *testing.T, svc core.TaskService) *frontend {
	t.Helper()

	socketPath := serverTestSocketPath(t)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errCh)
	})

	return &frontend{socketPath: socketPath}
}

func TestUnaryOperationsRoundTrip(t *testing.T) {
	t.Parallel()

	svc := newFakeTaskService()
	svc.tasks = []*core.Task{
		{ID: "task-1", DisplayName: "add retries", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "fix flake", Provider: core.ProviderClaude},
	}
	svc.activity["task-1"] = []core.TaskActivityEvent{
		{TaskID: "task-1", Role: core.TaskActivityRoleUser, Text: "add retries"},
		{TaskID: "task-1", Role: core.TaskActivityRoleAssistant, Text: "editing main.go"},
	}
	svc.usage["task-1"] = &core.TaskTokenUsage{InputTokens: 100, OutputTokens: 25}
	svc.prs["/tmp/repo"] = []core.RepoPullRequest{{Number: 7, Title: "retry flow", BranchName: "retries"}}
	svc.prStatuses["/tmp/repo|retries"] = &core.PRStatus{State: core.PRStateOpen, Number: 7}
	svc.setup = &core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex},
		Default:    core.ProviderCodex,
	}
	svc.detections = []core.ProviderDetection{{Provider: core.ProviderCodex, Ready: true}}
	svc.latest["task-1"] = &core.TaskStatusUpdate{
		TaskID:   "task-1",
		Provider: core.ProviderCodex,
		Phase:    core.TaskStatusPhaseWorking,
	}

	client := startTestFrontend(t, svc)
	ctx := context.Background()

	t.Run("list tasks", func(t *testing.T) {
		tasks, err := client.ListTasks(ctx)
		require.NoError(t, err)
		require.Len(t, tasks, 2)
		require.Equal(t, "task-1", tasks[0].ID)
	})

	t.Run("get task activity honors limit", func(t *testing.T) {
		events, err := client.GetTaskActivity(ctx, "task-1", 1)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, "editing main.go", events[0].Text)
	})

	t.Run("get task activity requires task id", func(t *testing.T) {
		_, err := client.GetTaskActivity(ctx, "  ", 5)
		require.ErrorContains(t, err, "task_id required")
	})

	t.Run("get task token usage", func(t *testing.T) {
		usage, err := client.GetTaskTokenUsage(ctx, "task-1")
		require.NoError(t, err)
		require.Equal(t, 100, usage.InputTokens)
	})

	t.Run("get task token usage requires task id", func(t *testing.T) {
		_, err := client.GetTaskTokenUsage(ctx, "")
		require.ErrorContains(t, err, "task_id required")
	})

	t.Run("list repo pull requests", func(t *testing.T) {
		prs, err := client.ListRepoPullRequests(ctx, "/tmp/repo")
		require.NoError(t, err)
		require.Len(t, prs, 1)
		require.Equal(t, 7, prs[0].Number)
	})

	t.Run("pull request status", func(t *testing.T) {
		status, err := client.PullRequestStatus(ctx, "/tmp/repo", "retries")
		require.NoError(t, err)
		require.Equal(t, core.PRStateOpen, status.State)
	})

	t.Run("pull request status maps missing PR to none", func(t *testing.T) {
		status, err := client.PullRequestStatus(ctx, "/tmp/repo", "no-such-branch")
		require.NoError(t, err)
		require.Equal(t, core.PRStateNone, status.State)
	})

	t.Run("latest task status", func(t *testing.T) {
		update, err := client.LatestTaskStatus(ctx, "task-1")
		require.NoError(t, err)
		require.Equal(t, core.TaskStatusPhaseWorking, update.Phase)
	})

	t.Run("latest task status returns nil update when none published", func(t *testing.T) {
		update, err := client.LatestTaskStatus(ctx, "task-2")
		require.NoError(t, err)
		require.Nil(t, update)
	})

	t.Run("get provider setup", func(t *testing.T) {
		setup, err := client.GetProviderSetup(ctx)
		require.NoError(t, err)
		require.Equal(t, core.ProviderCodex, setup.Default)
	})

	t.Run("save provider setup", func(t *testing.T) {
		next := core.ProviderSetup{
			Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
			Default:    core.ProviderClaude,
		}
		require.NoError(t, client.SaveProviderSetup(ctx, next))
		saved, err := client.GetProviderSetup(ctx)
		require.NoError(t, err)
		require.Equal(t, core.ProviderClaude, saved.Default)
	})

	t.Run("detect providers", func(t *testing.T) {
		detections, err := client.DetectProviders(ctx)
		require.NoError(t, err)
		require.Len(t, detections, 1)
		require.True(t, detections[0].Ready)
	})

	t.Run("switch task provider", func(t *testing.T) {
		task, err := client.SwitchTaskProvider(ctx, "task-2", core.ProviderCodex)
		require.NoError(t, err)
		require.Equal(t, core.ProviderCodex, task.Provider)
	})

	t.Run("switch task provider requires provider", func(t *testing.T) {
		_, err := client.SwitchTaskProvider(ctx, "task-2", " ")
		require.ErrorContains(t, err, "provider required")
	})

	t.Run("switch task provider surfaces service errors", func(t *testing.T) {
		_, err := client.SwitchTaskProvider(ctx, "no-such-task", core.ProviderCodex)
		require.ErrorContains(t, err, "task not found")
	})

	t.Run("reconnect task session", func(t *testing.T) {
		require.NoError(t, client.ReconnectTaskSession(ctx, "task-1"))
		svc.mu.Lock()
		defer svc.mu.Unlock()
		require.Equal(t, []string{"task-1"}, svc.reconnected)
	})

	t.Run("delete task", func(t *testing.T) {
		require.NoError(t, client.DeleteTask(ctx, "task-2"))
		svc.mu.Lock()
		defer svc.mu.Unlock()
		require.Equal(t, []string{"task-2"}, svc.deleted)
	})

	t.Run("delete task requires task id", func(t *testing.T) {
		require.ErrorContains(t, client.DeleteTask(ctx, ""), "task_id required")
	})

	t.Run("service errors surface as client errors", func(t *testing.T) {
		svc.mu.Lock()
		svc.errByOp["list_tasks"] = errors.New("repository offline")
		svc.mu.Unlock()
		_, err := client.ListTasks(ctx)
		require.ErrorContains(t, err, "repository offline")
		svc.mu.Lock()
		delete(svc.errByOp, "list_tasks")
		svc.mu.Unlock()
	})
}

func TestCreateTaskStreamRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("streams progress before terminal task", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createSteps = []core.TaskCreateProgressStep{
			core.TaskCreateProgressSuggestingName,
			core.TaskCreateProgressCreatingWorktree,
		}
		svc.createTask = &core.Task{ID: "task-1", DisplayName: "add retries"}
		client := startTestFrontend(t, svc)

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
		require.Len(t, got, 3)
		require.Equal(t, core.TaskCreateProgressSuggestingName, got[0].Progress.Step)
		require.Equal(t, core.TaskCreateProgressCreatingWorktree, got[1].Progress.Step)
		require.NotNil(t, got[2].Task)
		require.Equal(t, "task-1", got[2].Task.ID)
	})

	t.Run("terminal error carries partial task for retry", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createSteps = []core.TaskCreateProgressStep{core.TaskCreateProgressSuggestingName}
		svc.createTask = &core.Task{ID: "task-1", CreationStatus: core.TaskCreationStatusFailed}
		svc.createErr = errors.New("worktree creation failed")
		client := startTestFrontend(t, svc)

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
		terminal := got[1]
		require.ErrorContains(t, terminal.Err, "worktree creation failed")
		require.NotNil(t, terminal.Task)
		require.Equal(t, "task-1", terminal.Task.ID)
	})

	t.Run("retry task creation streams the same shape", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createSteps = []core.TaskCreateProgressStep{core.TaskCreateProgressStartingSession}
		svc.createTask = &core.Task{ID: "task-1"}
		client := startTestFrontend(t, svc)

		events, err := client.RetryTaskCreationStream(context.Background(), "task-1")
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		require.Len(t, got, 2)
		require.Equal(t, core.TaskCreateProgressStartingSession, got[0].Progress.Step)
		require.Equal(t, "task-1", got[1].Task.ID)
	})

	t.Run("create task requires input payload", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		conn, err := dialDaemonSocket(context.Background(), client.socketPath)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, json.NewEncoder(conn).Encode(socketRequest{Command: socketCommandCreateTask}))

		var resp socketEnvelope
		require.NoError(t, json.NewDecoder(conn).Decode(&resp))
		require.Equal(t, socketEnvelopeError, resp.Type)
		require.Contains(t, resp.Error, "input required")
	})
}

func TestSubscribeTaskStatusRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("streams updates after ack", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stream, err := client.SubscribeTaskStatus(ctx, "task-1")
		require.NoError(t, err)

		expected := core.TaskStatusUpdate{
			TaskID:       "task-1",
			Provider:     core.ProviderCodex,
			Phase:        core.TaskStatusPhaseWorking,
			RawEventName: "PreToolUse",
			ObservedAt:   time.Now().UTC(),
		}
		svc.updates <- expected

		select {
		case got := <-stream:
			require.Equal(t, expected, got)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for streamed task status update")
		}
	})

	t.Run("subscribe failure surfaces as error", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.subscribeErr = errors.New("subscribe failed")
		client := startTestFrontend(t, svc)

		_, err := client.SubscribeTaskStatus(context.Background(), "task-1")
		require.ErrorContains(t, err, "subscribe failed")
	})

	t.Run("requires task id", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		_, err := client.SubscribeTaskStatus(context.Background(), "  ")
		require.ErrorContains(t, err, "task_id required")
	})

	t.Run("client cancellation cancels the backend subscription", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		ctx, cancel := context.WithCancel(context.Background())
		_, err := client.SubscribeTaskStatus(ctx, "task-1")
		require.NoError(t, err)

		var backendCtx context.Context
		select {
		case backendCtx = <-svc.subscribeCtx:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for backend subscription")
		}

		cancel()

		select {
		case <-backendCtx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for backend subscribe context cancellation")
		}
	})
}

func TestHandshakeProbes(t *testing.T) {
	t.Parallel()

	client := startTestFrontend(t, newFakeTaskService())
	ctx := context.Background()

	require.NoError(t, probeSocketHealth(ctx, client.socketPath))
	require.NoError(t, probeFrontendProtocol(ctx, client.socketPath))
	require.NoError(t, probeFrontendBuildVersion(ctx, client.socketPath))
}

// TestHandshakeDetectsLegacyDaemon simulates a daemon still speaking the
// protocol-8 fat-envelope wire and asserts the probes report the mismatch
// that triggers the auto-restart. This is the one cross-version contract the
// wire must keep. See docs/adr/0002-version-locked-socket-protocol.md.
func TestHandshakeDetectsLegacyDaemon(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	listener, err := listenUnixSocket(context.Background(), socketPath)
	require.NoError(t, err)
	defer listener.Close()

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
				// Legacy protocol-8 daemons answered with top-level fields.
				switch req.Command {
				case socketCommandHealth:
					_, _ = conn.Write([]byte(`{"type":"health","ok":true}` + "\n"))
				case socketCommandProtocolVersion:
					_, _ = conn.Write([]byte(`{"type":"protocol_version","ok":true,"protocol_version":8}` + "\n"))
				case socketCommandFrontendBuildVersion:
					_, _ = conn.Write(
						[]byte(`{"type":"frontend_build_version","ok":true,"version":"some-old-build"}` + "\n"),
					)
				}
			}(conn)
		}
	}()

	ctx := context.Background()
	require.NoError(t, probeSocketHealth(ctx, socketPath))
	require.ErrorContains(t, probeFrontendProtocol(ctx, socketPath), "protocol version mismatch")
	require.ErrorContains(t, probeFrontendBuildVersion(ctx, socketPath), "build version mismatch")
}

func TestUnixSocketServer_RejectsUnsupportedCommand(t *testing.T) {
	t.Parallel()

	client := startTestFrontend(t, newFakeTaskService())

	conn, err := dialDaemonSocket(context.Background(), client.socketPath)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, json.NewEncoder(conn).Encode(socketRequest{Command: "no_such_command"}))

	var resp socketEnvelope
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	require.Equal(t, socketEnvelopeError, resp.Type)
	require.Contains(t, resp.Error, "unsupported command")
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
		errCh <- adapter.Serve(ctx, newFakeTaskService(), nil, nil)
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
