package codex

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewHookHTTPHandler_DecodesCodexHookAndDelegatesToTaskService(t *testing.T) {
	now := time.Date(2026, time.April, 20, 11, 0, 0, 0, time.UTC)
	payload := `{"cwd":"/tmp/repo-task","hook_event_name":"SessionStart","model":"gpt-5.4-codex","prompt":"fix the retry flow","session_id":"sess-1","source":"startup","transcript_path":"/tmp/codex-session.jsonl"}`
	service := core.NewMockHookEventHandler(t)
	service.EXPECT().HandleHookEvent(mock.Anything, core.HookEventInput{
		OccurredAt:     now,
		EventName:      "SessionStart",
		Provider:       core.ProviderCodex,
		RawPayloadJSON: payload,
		SessionID:      "sess-1",
		TranscriptPath: "/tmp/codex-session.jsonl",
		StartSource:    "startup",
		Model:          "gpt-5.4-codex",
		Cwd:            "/tmp/repo-task",
		PromptText:     "fix the retry flow",
	}).Return(nil).Once()
	handler := NewHookHTTPHandler(service, func() time.Time { return now }, "secret-token")

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/hook",
		bytes.NewBufferString(payload),
	)
	req.Header.Set("X-Codex-Hook-Event", "SessionStart")
	req.Header.Set(hookSecretHeader, "secret-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
}

func TestNewHookHTTPHandler_RejectsMissingSecret(t *testing.T) {
	handler := NewHookHTTPHandler(core.NewMockHookEventHandler(t), time.Now, "secret-token")

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/hook",
		bytes.NewBufferString(`{"hook_event_name":"SessionStart"}`),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestNewHookHTTPHandler_RejectsOversizedBody(t *testing.T) {
	handler := newHTTPHandler(time.Now, "secret-token", func(context.Context, core.HookEventInput) error {
		t.Fatal("handler should not receive oversized hook payload")
		return nil
	})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/hook",
		strings.NewReader(strings.Repeat("x", maxHookRequestBodyBytes+1)),
	)
	req.Header.Set(hookSecretHeader, "secret-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
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
