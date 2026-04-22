package codex

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestNewHookHTTPHandler_DecodesCodexHookAndDelegatesToTaskService(t *testing.T) {
	now := time.Date(2026, time.April, 20, 11, 0, 0, 0, time.UTC)
	var captured core.HookEventInput
	handler := NewHookHTTPHandler(&stubTaskService{
		handleHookEventFn: func(_ context.Context, input core.HookEventInput) error {
			captured = input
			return nil
		},
	}, func() time.Time { return now })

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
	require.Equal(t, core.HookEventInput{
		OccurredAt:     now,
		EventName:      "SessionStart",
		Provider:       core.ProviderCodex,
		RawPayloadJSON: `{"cwd":"/tmp/repo-task","hook_event_name":"SessionStart","prompt":"fix the retry flow","session_id":"session-1"}`,
		SessionID:      "session-1",
		Cwd:            "/tmp/repo-task",
		PromptText:     "fix the retry flow",
	}, captured)
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

type stubTaskService struct {
	handleHookEventFn func(context.Context, core.HookEventInput) error
}

func (s *stubTaskService) CreateTaskWithProgress(
	context.Context,
	core.CreateTaskInput,
	core.TaskCreateProgressReporter,
) (*core.Task, error) {
	return nil, nil
}

func (s *stubTaskService) ListTasks(context.Context) ([]*core.Task, error) {
	return nil, nil
}

func (s *stubTaskService) DeleteTask(context.Context, string) error {
	return nil
}

func (s *stubTaskService) ReconnectTaskSession(context.Context, string) error {
	return nil
}

func (s *stubTaskService) LatestTaskStatus(context.Context, string) (*core.TaskStatusUpdate, error) {
	return nil, nil
}

func (s *stubTaskService) SubscribeTaskStatus(context.Context, string) (<-chan core.TaskStatusUpdate, error) {
	return nil, nil
}

func (s *stubTaskService) HandleHookEvent(ctx context.Context, input core.HookEventInput) error {
	if s.handleHookEventFn == nil {
		return nil
	}
	return s.handleHookEventFn(ctx, input)
}
