package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqliterepo "agent/internal/adapters/repository/sqlite"
	"agent/internal/core"
	"github.com/stretchr/testify/require"
)

func TestServerHandleHook_IngestsManagedTaskEvent(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
		Provider:     "codex",
		Status:       core.TaskStatusRunning,
	})

	srv := newServer(repo, fixedClock(time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)))
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{
	  "session_id":"sess-1",
	  "cwd":"/tmp/repo-task-1",
	  "hook_event_name":"UserPromptSubmit",
	  "turn_id":"turn-1",
	  "prompt":"check the failing test"
	}`))
	req.Header.Set("X-Codex-Hook-Event", "UserPromptSubmit")

	rec := httptest.NewRecorder()
	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)

	summaries, err := repo.ListHookSessionSummaries(context.Background(), []string{task.ID})
	require.NoError(t, err)
	require.Contains(t, summaries, task.ID)
	require.Equal(t, core.HookRuntimePhasePrompted, summaries[task.ID].RuntimePhase)
	require.Equal(t, "sess-1", summaries[task.ID].SessionID)
	require.Equal(t, "turn-1", summaries[task.ID].CurrentTurnID)
	require.Equal(t, "check the failing test", summaries[task.ID].LastPromptText)

	events, err := repo.ListHookEvents(context.Background(), task.ID, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "UserPromptSubmit", events[0].EventName)
	require.Equal(t, "check the failing test", events[0].PromptText)
}

func TestServerHandleHook_IgnoresUnmanagedTaskCWD(t *testing.T) {
	repo := newTestRepository(t)
	srv := newServer(repo, fixedClock(time.Date(2026, 4, 8, 10, 1, 0, 0, time.UTC)))

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{
	  "session_id":"sess-x",
	  "cwd":"/tmp/unmanaged",
	  "hook_event_name":"SessionStart"
	}`))

	rec := httptest.NewRecorder()
	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)

	summaries, err := repo.ListHookSessionSummaries(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, summaries)
}

func TestServerHandleHook_IgnoresTypedUnmanagedHookEvent(t *testing.T) {
	srv := newServer(fakeHookEventIngestor{err: fmt.Errorf("wrap: %w", core.ErrUnmanagedHookEvent)}, fixedClock(time.Date(2026, 4, 8, 10, 1, 30, 0, time.UTC)))

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"hook_event_name":"SessionStart"}`))
	rec := httptest.NewRecorder()

	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
}

func TestSQLiteRepositoryIngestHookEvent_ReturnsTypedUnmanagedError(t *testing.T) {
	repo := newTestRepository(t)

	_, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		EventName: "SessionStart",
		Cwd:       "/tmp/unmanaged",
	})

	require.Error(t, err)
	require.ErrorIs(t, err, core.ErrUnmanagedHookEvent)
}

func TestServerHandleHook_PublishesRepositoryUpdateForManagedTask(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
		Provider:     "codex",
		Status:       core.TaskStatusRunning,
	})

	updates, cleanup, err := repo.SubscribeHookSessionUpdates(context.Background())
	require.NoError(t, err)
	defer cleanup()

	srv := newServer(repo, fixedClock(time.Date(2026, 4, 8, 10, 2, 0, 0, time.UTC)))
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{
	  "session_id":"sess-1",
	  "cwd":"/tmp/repo-task-1",
	  "hook_event_name":"PreToolUse",
	  "turn_id":"turn-1",
	  "tool_use_id":"tool-1",
	  "tool_input":{"command":"go test ./..."}
	}`))
	req.Header.Set("X-Codex-Hook-Event", "PreToolUse")

	rec := httptest.NewRecorder()
	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)

	select {
	case update := <-updates:
		require.Equal(t, task.ID, update.TaskID)
		require.Equal(t, core.HookRuntimePhaseRunningCommand, update.RuntimePhase)
		require.Equal(t, "go test ./...", update.LastCommandText)
		require.Equal(t, 1, update.CommandCount)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hook session update")
	}
}

func TestServerHandleHook_RejectsNonPOSTWithoutIngesting(t *testing.T) {
	repo := newTestRepository(t)
	srv := newServer(repo, fixedClock(time.Date(2026, 4, 8, 10, 3, 0, 0, time.UTC)))

	req := httptest.NewRequest(http.MethodGet, "/hook", strings.NewReader(`{"session_id":"sess-1"}`))
	rec := httptest.NewRecorder()

	srv.handleHook(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Equal(t, http.MethodPost, rec.Header().Get("Allow"))

	summaries, err := repo.ListHookSessionSummaries(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, summaries)
}

func TestResolveSQLitePath_IgnoresInvalidProviderAndUsesEnvOverride(t *testing.T) {
	t.Setenv("AGENT_PROVIDER", "definitely-invalid")
	t.Setenv("AGENT_SQLITE_PATH", "/tmp/custom-state.db")

	require.Equal(t, "/tmp/custom-state.db", resolveSQLitePath())
}

func TestResolveSQLitePath_UsesDefaultPathWhenEnvUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_SQLITE_PATH", "")

	require.Equal(t, filepath.Join(home, ".local", "share", "agent", "state.db"), resolveSQLitePath())
}

func newTestRepository(t *testing.T) *sqliterepo.Repository {
	t.Helper()

	repo, err := sqliterepo.NewRepository(sqliterepo.Config{
		Path: filepath.Join(t.TempDir(), "state.db"),
	})
	require.NoError(t, err)
	require.NoError(t, repo.IsAvailable(context.Background()))
	return repo
}

func seedTask(t *testing.T, repo *sqliterepo.Repository, task core.Task) *core.Task {
	t.Helper()

	now := time.Date(2026, 4, 8, 9, 30, 0, 0, time.UTC)
	if task.ID == "" {
		task.ID = "task-1"
	}
	if task.Slug == "" {
		task.Slug = task.ID
	}
	if task.DisplayName == "" {
		task.DisplayName = task.ID
	}
	if task.Prompt == "" {
		task.Prompt = "test prompt"
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
		task.BranchName = task.Slug
	}
	if task.WorktreePath == "" {
		task.WorktreePath = filepath.Join("/tmp", task.ID)
	}
	if task.TmuxSession == "" {
		task.TmuxSession = task.ID
	}
	if task.AgentWindowName == "" {
		task.AgentWindowName = "agent"
	}
	if task.EditorWindowName == "" {
		task.EditorWindowName = "editor"
	}
	if task.Provider == "" {
		task.Provider = "codex"
	}
	if task.Status == "" {
		task.Status = core.TaskStatusReady
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = now
	}

	task.WorktreeExists = true
	task.BranchExists = true
	task.SessionExists = true
	task.AgentWindowExists = true
	task.EditorWindowExists = true

	require.NoError(t, repo.CreateTask(context.Background(), &task))
	return &task
}

func fixedClock(ts time.Time) func() time.Time {
	return func() time.Time {
		return ts
	}
}

type fakeHookEventIngestor struct {
	err error
}

func (f fakeHookEventIngestor) IngestHookEvent(_ context.Context, _ core.HookEventInput) (*core.HookSessionSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &core.HookSessionSummary{}, nil
}
