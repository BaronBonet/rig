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

func TestNewHookHTTPHandler_DecodesHookProcessIdentity(t *testing.T) {
	cases := []struct {
		name       string
		pane       string
		tmux       string
		pid        string
		wantPane   string
		wantServer core.TmuxServer
		wantPID    int
	}{
		{
			name:       "inside tmux",
			pane:       "%44",
			tmux:       "/private/tmp/tmux-501/default,4722,3",
			pid:        "51234",
			wantPane:   "%44",
			wantServer: core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722},
			wantPID:    51234,
		},
		{name: "malformed values", pane: "%44", tmux: "not-tmux", pid: "abc"},
		{name: "outside tmux"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var received core.HookEventInput
			handler := newHTTPHandler(
				time.Now,
				"secret-token",
				func(_ context.Context, input core.HookEventInput) error {
					received = input
					return nil
				},
			)
			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/hook",
				bytes.NewBufferString(`{"hook_event_name":"SessionStart","session_id":"sess-1"}`),
			)
			req.Header.Set("X-Codex-Hook-Event", "SessionStart")
			req.Header.Set(hookSecretHeader, "secret-token")
			if tc.pane != "" {
				req.Header.Set("X-Rig-Tmux-Pane", tc.pane)
			}
			if tc.tmux != "" {
				req.Header.Set("X-Rig-Tmux", tc.tmux)
			}
			if tc.pid != "" {
				req.Header.Set("X-Rig-Hook-Pid", tc.pid)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusAccepted, rec.Code)
			require.Equal(t, "sess-1", received.SessionID)
			require.Equal(t, tc.wantPane, received.TmuxPane)
			require.Equal(t, tc.wantServer, received.TmuxServer)
			require.Equal(t, tc.wantPID, received.HookPID)
		})
	}
}

func TestDecodeHookEventInput_DecodesSubagentIdentity(t *testing.T) {
	now := time.Date(2026, time.April, 20, 11, 0, 0, 0, time.UTC)
	payload := []byte(`{"agent_id":"agent-456","agent_type":"worker","hook_event_name":"PostToolUse"}`)

	input := DecodeHookEventInput(func() time.Time { return now }, "", payload)

	require.Equal(t, "agent-456", input.AgentID)
	require.Equal(t, "worker", input.AgentType)
}

func TestRepositoryHookEventToTaskStatus_SessionEndDrivesNoStatus(t *testing.T) {
	now := time.Date(2026, time.October, 1, 10, 52, 0, 0, time.UTC)
	// Codex sends a null transcript path for a session without a rollout file.
	payload := []byte(`{"cwd":"/tmp/repo-task","hook_event_name":"SessionEnd","reason":"other",` +
		`"session_id":"session-123","transcript_path":null}`)
	repo := New(nil, Config{Binary: "codex"}, HookForwardingConfig{})

	input := DecodeHookEventInput(func() time.Time { return now }, "SessionEnd", payload)
	input.TaskID = "task-123"
	update, err := repo.HookEventToTaskStatus(input)

	require.NoError(t, err)
	require.Nil(t, update)
	require.Equal(t, "session-123", input.SessionID)
	require.Equal(t, "other", input.EndReason)
	require.Empty(t, input.TranscriptPath)
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

func TestRepositoryHookEventToTaskStatus_MapsSubagentPermissionRequestToWaitingForInput(t *testing.T) {
	repo := New(nil, Config{Binary: "codex"}, HookForwardingConfig{})

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:     "task-123",
		AgentID:    "agent-456",
		OccurredAt: time.Date(2026, time.April, 20, 11, 2, 0, 0, time.UTC),
		EventName:  "PermissionRequest",
		Provider:   core.ProviderCodex,
	})

	require.NoError(t, err)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "PermissionRequest",
		ObservedAt:   time.Date(2026, time.April, 20, 11, 2, 0, 0, time.UTC),
	}, update)
}

func TestRepositoryHookEventToTaskStatus_IgnoresSubagentWorkEvent(t *testing.T) {
	repo := New(nil, Config{Binary: "codex"}, HookForwardingConfig{})

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:     "task-123",
		AgentID:    "agent-456",
		OccurredAt: time.Date(2026, time.April, 20, 11, 2, 0, 0, time.UTC),
		EventName:  "PostToolUse",
		Provider:   core.ProviderCodex,
	})

	require.NoError(t, err)
	require.Nil(t, update)
}

func TestRepositoryHookEventToTaskStatus_SubagentStartDrivesNoStatus(t *testing.T) {
	now := time.Date(2026, time.October, 1, 10, 52, 0, 0, time.UTC)
	payload := []byte(`{"agent_id":"agent-456","agent_type":"worker","cwd":"/tmp/repo-task",` +
		`"hook_event_name":"SubagentStart","model":"gpt-5.5","permission_mode":"default",` +
		`"session_id":"session-123","transcript_path":"/tmp/codex-subagent.jsonl","turn_id":"turn-1"}`)
	repo := New(nil, Config{Binary: "codex"}, HookForwardingConfig{})

	input := DecodeHookEventInput(func() time.Time { return now }, "SubagentStart", payload)
	input.TaskID = "task-123"
	update, err := repo.HookEventToTaskStatus(input)

	require.NoError(t, err)
	require.Nil(t, update)
	require.Equal(t, "session-123", input.SessionID)
	require.Equal(t, "/tmp/codex-subagent.jsonl", input.TranscriptPath)
	require.Equal(t, "agent-456", input.AgentID)
}

func TestRepositoryHookEventToTaskStatus_SubagentStartWithoutAgentIDDrivesNoStatus(t *testing.T) {
	repo := New(nil, Config{Binary: "codex"}, HookForwardingConfig{})

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:     "task-123",
		OccurredAt: time.Date(2026, time.October, 1, 10, 52, 0, 0, time.UTC),
		EventName:  "SubagentStart",
		Provider:   core.ProviderCodex,
	})

	require.NoError(t, err)
	require.Nil(t, update)
}
