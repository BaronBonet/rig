package codex

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewHookHTTPHandler_DecodesCodexHookAndDelegatesToTaskService(t *testing.T) {
	now := time.Date(2026, time.April, 20, 11, 0, 0, 0, time.UTC)
	service := core.NewMockTaskService(t)
	service.EXPECT().HandleHookEvent(mock.Anything, core.HookEventInput{
		OccurredAt:     now,
		EventName:      "SessionStart",
		Provider:       core.ProviderCodex,
		RawPayloadJSON: `{"cwd":"/tmp/repo-task","hook_event_name":"SessionStart","prompt":"fix the retry flow","session_id":"session-1"}`,
		SessionID:      "session-1",
		Cwd:            "/tmp/repo-task",
		PromptText:     "fix the retry flow",
	}).Return(nil).Once()
	handler := NewHookHTTPHandler(service, func() time.Time { return now })

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/hook",
		bytes.NewBufferString(
			`{"cwd":"/tmp/repo-task","hook_event_name":"SessionStart","prompt":"fix the retry flow","session_id":"session-1"}`,
		),
	)
	req.Header.Set("X-Codex-Hook-Event", "SessionStart")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
}

func TestRepositoryHookEventToTaskStatus_MapsCodexEvent(t *testing.T) {
	repo := New(nil, Config{Binary: "codex"}, HookForwardingConfig{})

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:     "task-123",
		OccurredAt: time.Date(2026, time.April, 20, 11, 1, 0, 0, time.UTC),
		EventName:  "PostToolUse",
		Provider:   core.ProviderCodex,
	})
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 20, 11, 1, 0, 0, time.UTC),
	}, update)
}

func TestRepositoryHookEventToTaskStatus_MapsPermissionRequestToWaitingForInput(t *testing.T) {
	repo := New(nil, Config{Binary: "codex"}, HookForwardingConfig{})

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:     "task-123",
		OccurredAt: time.Date(2026, time.April, 20, 11, 2, 0, 0, time.UTC),
		EventName:  "PermissionRequest",
		Provider:   core.ProviderCodex,
	})
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "PermissionRequest",
		ObservedAt:   time.Date(2026, time.April, 20, 11, 2, 0, 0, time.UTC),
	}, update)
}
