package claude

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
)

func TestNewHookHTTPHandler_TakesTaskIDFromForwarderHeader(t *testing.T) {
	payload := `{"cwd":"/tmp/shared","hook_event_name":"Stop","session_id":"sess-1"}`
	service := core.NewMockHookEventHandler(t)
	service.EXPECT().HandleHookEvent(mock.Anything, mock.MatchedBy(func(input core.HookEventInput) bool {
		return input.TaskID == "task-123" && input.Cwd == "/tmp/shared"
	})).Return(nil).Once()
	handler := NewHookHTTPHandler(service, fixedNow, "secret-token")

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/hook",
		bytes.NewBufferString(payload),
	)
	req.Header.Set(hookEventHeader, "Stop")
	req.Header.Set(providerkit.TaskIDHeader, "task-123")
	req.Header.Set(hookSecretHeader, "secret-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
}

func TestNewHookHTTPHandler_LeavesTaskIDEmptyWithoutForwarderHeader(t *testing.T) {
	payload := `{"cwd":"/tmp/repo-task","hook_event_name":"Stop","session_id":"sess-1"}`
	service := core.NewMockHookEventHandler(t)
	service.EXPECT().HandleHookEvent(mock.Anything, mock.MatchedBy(func(input core.HookEventInput) bool {
		return input.TaskID == "" && input.Cwd == "/tmp/repo-task"
	})).Return(nil).Once()
	handler := NewHookHTTPHandler(service, fixedNow, "secret-token")

	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/hook",
		bytes.NewBufferString(payload),
	)
	req.Header.Set(hookEventHeader, "Stop")
	req.Header.Set(hookSecretHeader, "secret-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
}
