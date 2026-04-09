package observer

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqliterepo "agent/internal/adapters/repository/sqlite"
	"agent/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestServe_HealthCheckOverUnixSocket(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("observer-%d.sock", time.Now().UnixNano()))
	hookListener := mustListenTCP(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServerConfig{
			SocketPath:     socketPath,
			HookListenAddr: hookListener.Addr().String(),
			HookListener:   hookListener,
			HookIngestor:   &stubObserverHookIngestor{},
			Hub:            NewHub(),
		})
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return false
		default:
		}
		return socketHealthOK(socketPath) == nil
	}, 2*time.Second, 20*time.Millisecond)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)

	_, err := os.Stat(socketPath)
	require.Error(t, err)
}

func TestObserverHookEndpoint_PersistsEventAndPublishesTaskUpdate(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("observer-%d.sock", time.Now().UnixNano()))
	hookListener := mustListenTCP(t)
	repo := mustCreateTaskRepository(t)
	task := mustSeedTask(t, repo, core.Task{
		ID:               "task-1",
		Prompt:           "add observer hook ingest",
		DisplayName:      "observer hook ingest",
		Slug:             "observer-hook-ingest",
		RepoRoot:         "/tmp/repo",
		RepoName:         "repo",
		BaseBranch:       "main",
		BranchName:       "feat/observer-hook-ingest",
		WorktreePath:     "/tmp/repo-observer-hook-ingest",
		TmuxSession:      "repo-observer-hook-ingest",
		Provider:         "codex",
		Status:           core.TaskStatusRunning,
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})
	hub := NewHub()
	updates, release := mustSubscribeHub(t, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServerConfig{
			SocketPath:     socketPath,
			HookListenAddr: hookListener.Addr().String(),
			HookListener:   hookListener,
			HookIngestor:   repo,
			Hub:            hub,
		})
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return false
		default:
		}
		status, err := unixHTTPStatus(
			hookListener.Addr().String(),
			http.MethodPost,
			"/hook",
			`{"cwd":"/tmp/worktree","session_id":"sess-1","hook_event_name":"SessionStart"}`,
		)
		if err != nil {
			return false
		}
		return status == http.StatusAccepted
	}, 2*time.Second, 20*time.Millisecond)

	req, err := http.NewRequest(
		http.MethodPost,
		"http://"+hookListener.Addr().String()+"/hook",
		bytes.NewReader(
			[]byte(
				`{"cwd":"`+task.WorktreePath+`","session_id":"sess-1","hook_event_name":"SessionStart","model":"gpt-5"}`,
			),
		),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Codex-Hook-Event", "SessionStart")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var update core.ObserverTaskUpdate
	select {
	case update = <-updates:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for hub update")
	}
	require.Equal(t, task.ID, update.TaskID)

	events, err := repo.ListHookEvents(t.Context(), task.ID, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "SessionStart", events[0].EventName)

	summaries, err := repo.ListHookSessionSummaries(t.Context(), []string{task.ID})
	require.NoError(t, err)
	require.Contains(t, summaries, task.ID)
	require.Equal(t, "sess-1", summaries[task.ID].SessionID)

	release()
	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)
}

func TestObserverDropsUnmanagedHookEventsWithoutBroadcast(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("observer-%d.sock", time.Now().UnixNano()))
	hookListener := mustListenTCP(t)
	hub := NewHub()
	updates, release := mustSubscribeHub(t, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServerConfig{
			SocketPath:     socketPath,
			HookListenAddr: hookListener.Addr().String(),
			HookListener:   hookListener,
			HookIngestor:   &stubObserverHookIngestor{err: core.ErrUnmanagedHookEvent},
			Hub:            hub,
		})
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return false
		default:
		}
		return socketHealthOK(socketPath) == nil
	}, 2*time.Second, 20*time.Millisecond)

	req, err := http.NewRequest(
		http.MethodPost,
		"http://"+hookListener.Addr().String()+"/hook",
		strings.NewReader(`{"hook_event_name":"Stop"}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Codex-Hook-Event", "Stop")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	select {
	case update := <-updates:
		t.Fatalf("unexpected hub update: %+v", update)
	case <-time.After(200 * time.Millisecond):
	}

	release()
	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)
}

func TestServe_RefreshLoopPublishesTaskUpdatesFromWatcher(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("observer-%d.sock", time.Now().UnixNano()))
	hookListener := mustListenTCP(t)
	repo := mustCreateTaskRepository(t)
	task := mustSeedTask(t, repo, core.Task{
		ID:               "task-1",
		DisplayName:      "watcher refresh",
		Slug:             "watcher-refresh",
		WorktreePath:     "/tmp/watcher-refresh",
		TmuxSession:      "repo-watcher-refresh",
		Provider:         "codex",
		Status:           core.TaskStatusRunning,
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})
	now := time.Date(2026, 4, 9, 12, 20, 0, 0, time.UTC)
	monitor := core.NewMockRuntimeMonitor(t)
	monitor.EXPECT().Snapshot(mock.Anything, mock.MatchedBy(func(in *core.Task) bool {
		return in != nil && in.ID == task.ID
	})).Return(core.RuntimeSnapshot{
		SessionName:       task.TmuxSession,
		PaneID:            "%24",
		ForegroundCommand: "go",
		HadAgentBinding:   true,
		ObservedAt:        now,
	}, nil).Maybe()
	monitor.EXPECT().Close().Return(nil).Once()

	watcher := NewTMuxWatcher(TMuxWatcherConfig{
		Tasks:   repo,
		Monitor: monitor,
		Repo:    repo,
		Providers: map[string]core.ProviderClient{
			"codex": stubTMuxWatcherProvider{runtimeState: core.RuntimeStateRunning},
		},
	})
	hub := NewHub()
	updates, release := mustSubscribeHub(t, hub)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServerConfig{
			SocketPath:      socketPath,
			HookListenAddr:  hookListener.Addr().String(),
			HookListener:    hookListener,
			HookIngestor:    repo,
			Watcher:         watcher,
			Hub:             hub,
			RefreshInterval: 25 * time.Millisecond,
		})
	}()

	select {
	case update := <-updates:
		require.Equal(t, task.ID, update.TaskID)
		require.Equal(t, core.DisplayStatusWorking, update.DisplayStatus)
		require.Equal(t, core.DisplayActivityCommand, update.DisplayActivity)
		require.Equal(t, now, update.LastActivityAt)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tmux watcher update")
	}

	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)
}

func mustListenTCP(t *testing.T) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return listener
}

func unixHTTPStatus(address, method, target, body string) (int, error) {
	conn, err := net.Dial("unix", address)
	if err != nil {
		conn, err = net.Dial("tcp", address)
		if err != nil {
			return 0, err
		}
	}
	defer conn.Close()

	if method == http.MethodGet {
		body = ""
	}

	request := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: local\r\nConnection: close\r\n", method, target)
	if body != "" {
		request += fmt.Sprintf("Content-Type: application/json\r\nContent-Length: %d\r\n", len(body))
	}
	request += "\r\n" + body

	if _, err := io.WriteString(conn, request); err != nil {
		return 0, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, nil
}

type stubObserverHookIngestor struct {
	called bool
	input  core.HookEventInput
	err    error
}

func (s *stubObserverHookIngestor) IngestHookEvent(
	_ context.Context,
	input core.HookEventInput,
) (*core.HookSessionSummary, error) {
	s.called = true
	s.input = input
	return nil, s.err
}

func mustCreateTaskRepository(t *testing.T) *sqliterepo.Repository {
	t.Helper()

	repo, err := sqliterepo.NewRepository(sqliterepo.Config{Path: filepath.Join(t.TempDir(), "state.db")})
	require.NoError(t, err)
	return repo
}

func mustSeedTask(t *testing.T, repo *sqliterepo.Repository, task core.Task) *core.Task {
	t.Helper()

	now := time.Now().UTC()
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}
	if task.AgentWindowName == "" {
		task.AgentWindowName = "agent"
	}
	if task.EditorWindowName == "" {
		task.EditorWindowName = "editor"
	}
	if task.DisplayName == "" {
		task.DisplayName = task.ID
	}
	if task.Slug == "" {
		task.Slug = task.ID
	}
	if task.RepoRoot == "" {
		task.RepoRoot = "/tmp/repo"
	}
	if task.RepoName == "" {
		task.RepoName = "repo"
	}
	if task.BaseBranch == "" {
		task.BaseBranch = "main"
	}
	if task.BranchName == "" {
		task.BranchName = "feat/" + task.Slug
	}
	if task.WorktreePath == "" {
		task.WorktreePath = filepath.Join("/tmp", task.Slug)
	}
	if task.TmuxSession == "" {
		task.TmuxSession = task.Slug
	}

	require.NoError(t, repo.CreateTask(context.Background(), &task))
	return &task
}

func mustSubscribeHub(t *testing.T, hub *Hub) (<-chan core.ObserverTaskUpdate, func()) {
	t.Helper()

	updates, release := hub.Subscribe(t.Context())
	require.NotNil(t, updates)
	require.NotNil(t, release)
	return updates, release
}

func socketHealthOK(socketPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return dialSocketHealth(ctx, socketPath)
}
