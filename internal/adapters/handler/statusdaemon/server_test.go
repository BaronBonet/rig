package statusdaemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestDaemon_ImplementsTaskFrontend(t *testing.T) {
	var _ core.TaskFrontend = (*Daemon)(nil)
}

func TestSocketServer_CreateTaskCallsTaskService(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	hookListener := testHookListener(t)
	svc := &stubTaskService{
		createTaskFn: func(_ context.Context, input core.CreateTaskInput) (*core.Task, error) {
			require.Equal(t, core.CreateTaskInput{
				Cwd:      "/tmp/repo",
				Prompt:   "add retries",
				Provider: "codex",
			}, input)
			return &core.Task{
				ID:          "task-1",
				DisplayName: "add retries",
			}, nil
		},
	}
	daemon := New(Config{
		SocketPath:   socketPath,
		Service:      svc,
		HookListener: hookListener,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Serve(ctx)
	}()
	waitForSocketServer(t, socketPath)

	task, err := createTask(context.Background(), socketPath, core.CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add retries",
		Provider: "codex",
	})
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, "task-1", task.ID)
	require.Equal(t, "add retries", task.DisplayName)

	cancel()
	require.NoError(t, <-errCh)
}

func TestSocketServer_SubscribeTaskStatusStreamsMatchingUpdates(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	hookListener := testHookListener(t)
	updates := make(chan core.TaskStatusUpdate, 1)
	svc := &stubTaskService{
		subscribeTaskStatusFn: func(_ context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
			require.Equal(t, "task-1", taskID)
			return updates, nil
		},
	}
	daemon := New(Config{
		SocketPath:   socketPath,
		Service:      svc,
		HookListener: hookListener,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- daemon.Serve(ctx)
	}()
	waitForSocketServer(t, socketPath)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	stream, cleanup, err := subscribeTaskStatus(streamCtx, socketPath, "task-1")
	require.NoError(t, err)
	defer cleanup()

	expected := core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.AgentProviderCodex,
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

func TestCodexHookHandler_PublishesMappedTaskStatusUpdate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 19, 15, 0, 0, 0, time.UTC)
	var published core.TaskStatusUpdate
	svc := &stubTaskService{
		publishTaskStatusFn: func(_ context.Context, update core.TaskStatusUpdate) error {
			published = update
			return nil
		},
	}
	tasks := &stubTaskRepository{
		listTasksFn: func(_ context.Context) ([]*core.Task, error) {
			return []*core.Task{{
				ID:           "task-1",
				WorktreePath: "/tmp/repo-task",
			}}, nil
		},
	}
	daemon := New(Config{
		Service: svc,
		Tasks:   tasks,
		Now:     func() time.Time { return now },
	})

	req := httptestNewRequest(t, http.MethodPost, "/codex-hook", map[string]any{
		"cwd":             "/tmp/repo-task",
		"hook_event_name": "SessionStart",
		"session_id":      "session-1",
	})
	req.Header.Set("X-Codex-Hook-Event", "SessionStart")

	rec := newRecorder()
	daemon.codexHookHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Equal(t, core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.AgentProviderCodex,
		Phase:        core.TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
		ObservedAt:   now,
	}, published)
}

type stubTaskService struct {
	createTaskFn          func(context.Context, core.CreateTaskInput) (*core.Task, error)
	latestTaskStatusFn    func(context.Context, string) (*core.TaskStatusUpdate, error)
	subscribeTaskStatusFn func(context.Context, string) (<-chan core.TaskStatusUpdate, error)
	publishTaskStatusFn   func(context.Context, core.TaskStatusUpdate) error
}

func (s *stubTaskService) CreateTask(ctx context.Context, input core.CreateTaskInput) (*core.Task, error) {
	return s.createTaskFn(ctx, input)
}

func (s *stubTaskService) LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	if s.latestTaskStatusFn == nil {
		return nil, nil
	}
	return s.latestTaskStatusFn(ctx, taskID)
}

func (s *stubTaskService) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	return s.subscribeTaskStatusFn(ctx, taskID)
}

func (s *stubTaskService) PublishTaskStatus(ctx context.Context, update core.TaskStatusUpdate) error {
	if s.publishTaskStatusFn == nil {
		return nil
	}
	return s.publishTaskStatusFn(ctx, update)
}

type stubTaskRepository struct {
	listTasksFn func(context.Context) ([]*core.Task, error)
}

func (s *stubTaskRepository) CreateTask(context.Context, *core.Task) error { return nil }
func (s *stubTaskRepository) UpdateTask(context.Context, *core.Task) error { return nil }
func (s *stubTaskRepository) ListTasks(ctx context.Context) ([]*core.Task, error) {
	if s.listTasksFn == nil {
		return nil, nil
	}
	return s.listTasksFn(ctx)
}
func (s *stubTaskRepository) UpsertTaskStatus(context.Context, core.TaskStatusUpdate) error {
	return nil
}
func (s *stubTaskRepository) LatestTaskStatus(context.Context, string) (*core.TaskStatusUpdate, error) {
	return nil, nil
}
func (s *stubTaskRepository) SubscribeTaskStatus(context.Context, string) (<-chan core.TaskStatusUpdate, error) {
	return nil, nil
}

func waitForSocketServer(t *testing.T, socketPath string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := dialSocketHealth(context.Background(), socketPath); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("socket server at %s did not become healthy", socketPath)
}

func testSocketPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(os.TempDir(), "rig-statusdaemon-"+time.Now().UTC().Format("20060102150405.000000000")+".sock")
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	return path
}

func testHookListener(t *testing.T) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
	})

	return listener
}

func createTask(ctx context.Context, socketPath string, input core.CreateTaskInput) (*core.Task, error) {
	conn, err := net.Dial("unix", socketPath)
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

func subscribeTaskStatus(ctx context.Context, socketPath string, taskID string) (<-chan core.TaskStatusUpdate, func(), error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
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

	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	require.NoError(t, err)
	return req
}
