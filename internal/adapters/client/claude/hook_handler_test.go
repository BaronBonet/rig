package claude

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func fixedNow() time.Time {
	return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
}

func TestDecodeHookEventInput_DecodesSessionStartPayload(t *testing.T) {
	body := []byte(`{
		"session_id": "sess-1",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "/tmp/repo-task",
		"hook_event_name": "SessionStart",
		"source": "startup"
	}`)

	input := DecodeHookEventInput(fixedNow, "SessionStart", body)

	require.Equal(t, core.ProviderClaude, input.Provider)
	require.Equal(t, "SessionStart", input.EventName)
	require.Equal(t, "sess-1", input.SessionID)
	require.Equal(t, "/tmp/transcript.jsonl", input.TranscriptPath)
	require.Equal(t, "/tmp/repo-task", input.Cwd)
	require.Equal(t, "startup", input.StartSource)
	require.Empty(t, input.TaskID)
	require.Equal(t, fixedNow(), input.OccurredAt)
}

func TestDecodeHookEventInput_DecodesSessionEndReason(t *testing.T) {
	body := []byte(`{
		"session_id": "sess-1",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "/tmp/repo-task",
		"hook_event_name": "SessionEnd",
		"reason": "prompt_input_exit"
	}`)

	input := DecodeHookEventInput(fixedNow, "SessionEnd", body)

	require.Equal(t, "SessionEnd", input.EventName)
	require.Equal(t, "sess-1", input.SessionID)
	require.Equal(t, "prompt_input_exit", input.EndReason)
	require.Empty(t, input.StartSource)
}

func TestHookHTTPHandler_DecodesHookProcessIdentity(t *testing.T) {
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
				fixedNow,
				"secret-token",
				func(_ context.Context, input core.HookEventInput) error {
					received = input
					return nil
				},
			)
			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				claudeHookPath,
				bytes.NewBufferString(`{"hook_event_name":"SessionStart","session_id":"sess-1"}`),
			)
			req.Header.Set("X-Claude-Hook-Event", "SessionStart")
			req.Header.Set("X-Rig-Hook-Secret", "secret-token")
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

func TestDecodeHookEventInput_DecodesPromptAndToolPayloads(t *testing.T) {
	prompt := DecodeHookEventInput(fixedNow, "", []byte(`{
		"session_id": "sess-1",
		"hook_event_name": "UserPromptSubmit",
		"prompt": "fix the billing retry flow"
	}`))
	require.Equal(t, "UserPromptSubmit", prompt.EventName)
	require.Equal(t, "fix the billing retry flow", prompt.PromptText)

	tool := DecodeHookEventInput(fixedNow, "PostToolUse", []byte(`{
		"session_id": "sess-1",
		"tool_input": {"command": "go test ./..."},
		"tool_response": "ok"
	}`))
	require.Equal(t, "PostToolUse", tool.EventName)
	require.Equal(t, "go test ./...", tool.CommandText)
	require.Equal(t, "ok", tool.CommandResultText)
}

func TestDecodeHookEventInput_CountsInFlightBackgroundTasksOnStop(t *testing.T) {
	// Shape captured from Claude Code 2.1.286. A Monitor tool watch is
	// reported as a "shell" task.
	input := DecodeHookEventInput(fixedNow, "Stop", []byte(`{
		"session_id": "sess-1",
		"hook_event_name": "Stop",
		"stop_hook_active": false,
		"background_tasks": [
			{"id": "bc4mtriyg", "type": "shell", "status": "running",
			 "description": "QA import run", "command": "heroku logs --tail"},
			{"id": "a1", "type": "subagent", "status": "running", "agent_type": "Explore"},
			{"id": "a2", "type": "subagent", "status": "pending", "agent_type": "general-purpose"},
			{"id": "m1", "type": "monitor", "status": "running", "server": "ci"},
			{"id": "w1", "type": "workflow", "status": "running", "name": "review"},
			{"id": "t1", "type": "teammate", "status": "running"},
			{"id": "d1", "type": "dream", "status": "running"},
			{"id": "a3", "type": "subagent", "status": "completed"}
		],
		"session_crons": []
	}`))

	require.Equal(t, core.TaskBackgroundWork{
		Subagents: 2,
		Shells:    1,
		Monitors:  1,
		Workflows: 1,
		Other:     1,
	}, input.BackgroundWork)
}

func TestDecodeHookEventInput_DecodesNotificationTypeAndSubagentID(t *testing.T) {
	input := DecodeHookEventInput(fixedNow, "Notification", []byte(`{
		"session_id": "sess-1",
		"agent_id": "agent-7",
		"agent_type": "Explore",
		"notification_type": "permission_prompt",
		"message": "Claude needs your permission to use Bash"
	}`))

	require.Equal(t, "permission_prompt", input.NotificationType)
	require.Equal(t, "agent-7", input.AgentID)
	require.Equal(t, "Explore", input.AgentType)
}

func TestDecodeHookEventInput_ToleratesMalformedPayload(t *testing.T) {
	input := DecodeHookEventInput(fixedNow, "", []byte(`not-json`))

	require.Equal(t, core.ProviderClaude, input.Provider)
	require.Equal(t, "unknown", input.EventName)
}

// Claude Code also fires UserPromptSubmit for the turns it starts on its own,
// with what started the turn as the prompt: a task notification when
// background work reports back, such as a subagent or shell finishing or a
// monitor emitting an event, or a message from a background subagent or
// another Claude session.
func TestDecodeHookEventInput_ATurnClaudeCodeStartsIsNoPromptButStillMeansWorking(t *testing.T) {
	cases := map[string]string{
		"task notification": `<task-notification>\n<task-id>bap0vqoch</task-id>\n` +
			`<status>completed</status>\n</task-notification>`,
		"subagent message": `<agent-message from=\"a014a756436d9f22b\"> [Subagent hand-back] ` +
			`The text below is the final report.</agent-message>`,
		"cross-session message": `<cross-session-message from=\"sess-2\" from-name=\"review\"> ` +
			`The review is done.</cross-session-message>`,
	}
	repo := &repository{binary: "claude"}
	for name, prompt := range cases {
		t.Run(name, func(t *testing.T) {
			input := DecodeHookEventInput(fixedNow, "UserPromptSubmit", []byte(`{
				"session_id": "sess-1",
				"hook_event_name": "UserPromptSubmit",
				"prompt": "`+prompt+`"
			}`))
			require.Equal(t, "UserPromptSubmit", input.EventName)
			require.Equal(t, "sess-1", input.SessionID, "the payload decodes")
			require.Empty(t, input.PromptText)

			// The task service resolves the task from the hook's working
			// directory.
			input.TaskID = "task-1"
			update, err := repo.HookEventToTaskStatus(input)
			require.NoError(t, err)
			require.NotNil(t, update)
			require.Equal(t, core.TaskStatusPhaseWorking, update.Phase)
		})
	}

	// A prompt that only mentions one of the tags is still the user's.
	typed := DecodeHookEventInput(fixedNow, "UserPromptSubmit", []byte(`{
		"prompt": "why did the <task-notification> show up as my prompt?"
	}`))
	require.Equal(t, "why did the <task-notification> show up as my prompt?", typed.PromptText)
}

func TestHookEventToTaskStatus_MapsClaudeEventsToPhases(t *testing.T) {
	repo := &repository{binary: "claude"}

	cases := []struct {
		event string
		phase core.TaskStatusPhase
	}{
		{"SessionStart", core.TaskStatusPhaseStarting},
		{"UserPromptSubmit", core.TaskStatusPhaseWorking},
		{"PreToolUse", core.TaskStatusPhaseWorking},
		{"PostToolUse", core.TaskStatusPhaseWorking},
		{"Stop", core.TaskStatusPhaseWaitingForInput},
		{"StopFailure", core.TaskStatusPhaseWaitingForInput},
		{"Notification", core.TaskStatusPhaseWaitingForInput},
	}
	for _, tc := range cases {
		update, err := repo.HookEventToTaskStatus(core.HookEventInput{
			TaskID:     "task-1",
			EventName:  tc.event,
			OccurredAt: fixedNow(),
		})
		require.NoError(t, err, tc.event)
		require.NotNil(t, update, tc.event)
		require.Equal(t, tc.phase, update.Phase, tc.event)
		require.Equal(t, core.ProviderClaude, update.Provider, tc.event)
	}
}

func TestHookEventToTaskStatus_StopWithBackgroundWorkReportsWorkingInBackground(t *testing.T) {
	repo := &repository{binary: "claude"}
	work := core.TaskBackgroundWork{Subagents: 1, Shells: 1}

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:         "task-1",
		EventName:      "Stop",
		OccurredAt:     fixedNow(),
		BackgroundWork: work,
	})

	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, core.TaskStatusPhaseWorkingInBackground, update.Phase)
	require.Equal(t, work, update.BackgroundWork)
	require.Equal(t, "Stop", update.RawEventName)
}

func TestHookEventToTaskStatus_StopWithoutBackgroundWorkNeedsInput(t *testing.T) {
	repo := &repository{binary: "claude"}

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:     "task-1",
		EventName:  "Stop",
		OccurredAt: fixedNow(),
	})

	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, update.Phase)
	require.True(t, update.BackgroundWork.IsZero())
}

func TestHookEventToTaskStatus_APIErrorTurnEndNeedsInput(t *testing.T) {
	repo := &repository{binary: "claude"}
	// Payload shape from the Claude Code hooks reference. StopFailure fires
	// instead of Stop, so it is the only evidence that the turn ended.
	input := DecodeHookEventInput(fixedNow, "StopFailure", []byte(`{
		"session_id": "sess-1",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "/tmp/repo-task",
		"hook_event_name": "StopFailure",
		"error": "rate_limit",
		"error_details": "429 Too Many Requests",
		"last_assistant_message": "API Error: Rate limit reached"
	}`))
	input.TaskID = "task-1"

	update, err := repo.HookEventToTaskStatus(input)

	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, update.Phase)
	require.Equal(t, "StopFailure", update.RawEventName)
	require.Equal(t, fixedNow(), update.ObservedAt)
}

func TestHookEventToTaskStatus_OnlyNotificationsAskingForInputDriveStatus(t *testing.T) {
	repo := &repository{binary: "claude"}

	for _, notificationType := range []string{
		"permission_prompt",
		"worker_permission_prompt",
		"elicitation_dialog",
		"elicitation_url_dialog",
		"agent_needs_input",
	} {
		update, err := repo.HookEventToTaskStatus(core.HookEventInput{
			TaskID:           "task-1",
			EventName:        "Notification",
			NotificationType: notificationType,
		})
		require.NoError(t, err, notificationType)
		require.NotNil(t, update, notificationType)
		require.Equal(t, core.TaskStatusPhaseWaitingForInput, update.Phase, notificationType)
	}

	// idle_prompt fires a minute after every turn end, including while
	// background work is still running.
	for _, notificationType := range []string{"idle_prompt", "auth_success", "agent_completed"} {
		update, err := repo.HookEventToTaskStatus(core.HookEventInput{
			TaskID:           "task-1",
			EventName:        "Notification",
			NotificationType: notificationType,
		})
		require.NoError(t, err, notificationType)
		require.Nil(t, update, notificationType)
	}
}

func TestHookEventToTaskStatus_SubagentHooksDriveStatusOnlyWhenAskingForInput(t *testing.T) {
	repo := &repository{binary: "claude"}

	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "StopFailure"} {
		update, err := repo.HookEventToTaskStatus(core.HookEventInput{
			TaskID:    "task-1",
			EventName: event,
			AgentID:   "agent-7",
		})
		require.NoError(t, err, event)
		require.Nil(t, update, event)
	}

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:           "task-1",
		EventName:        "Notification",
		AgentID:          "agent-7",
		NotificationType: "permission_prompt",
	})
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, update.Phase)
}

func TestHookEventToTaskStatus_IgnoresUnmappedEventsAndMissingTask(t *testing.T) {
	repo := &repository{binary: "claude"}

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:    "task-1",
		EventName: "SubagentStop",
	})
	require.NoError(t, err)
	require.Nil(t, update)

	// SessionEnd ends or keeps an agent session but drives no phase.
	update, err = repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:    "task-1",
		EventName: core.HookEventSessionEnd,
		EndReason: "prompt_input_exit",
	})
	require.NoError(t, err)
	require.Nil(t, update)

	_, err = repo.HookEventToTaskStatus(core.HookEventInput{EventName: "Stop"})
	require.ErrorIs(t, err, core.ErrUnmanagedHookEvent)
}
