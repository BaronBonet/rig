package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// removedAt is when the removal tests remove an agent, after their agents'
// hook events.
var removedAt = agentTestStart.Add(30 * time.Minute)

// newTwoAgentHarness is the agent session harness with Claude, in %1, and
// then Codex, in %2, working in task-1's Session. It returns the agent
// sessions' IDs, oldest first. The observation's clock stands at removedAt.
func newTwoAgentHarness(t *testing.T) (*testTaskServiceHarness, string, string) {
	t.Helper()
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "codex")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 102, "%2", 1),
		TaskStatusPhaseWorking)
	svc.observation.now = func() time.Time { return removedAt }
	require.Len(t, svc.taskRepo.agentSessions, 2)
	return svc, svc.taskRepo.agentSessions[0].ID, svc.taskRepo.agentSessions[1].ID
}

func TestRemoveAgentSession_ClosesItsPaneAndEndsOnlyThatAgent(t *testing.T) {
	svc, claudeID, codexID := newTwoAgentHarness(t)
	observer := svc.manualStatusObserver()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	require.Len(t, receiveTaskStatus(t, stream).AgentSessions, 2)

	require.NoError(t, svc.service.RemoveAgentSession(t.Context(), "task-1", codexID))

	require.Equal(t, []TmuxPaneRef{{Server: agentTestServer, ID: "%2"}}, svc.sessionClient.closedPanes)
	sessions := svc.taskRepo.agentSessions
	require.Equal(t, claudeID, sessions[0].ID)
	require.True(t, sessions[0].IsOpen())
	require.Equal(t, codexID, sessions[1].ID)
	require.Equal(t, removedAt, sessions[1].EndedAt)
	// The task's status drops the agent at once, without waiting for a
	// status cycle.
	update := receiveTaskStatus(t, stream)
	require.Len(t, update.AgentSessions, 1)
	require.Equal(t, claudeID, update.AgentSessions[0].ID)
	require.Equal(t, claudeID, update.LeadAgentSessionID)
	// The task, its Session and its workspace stay.
	require.Nil(t, svc.sessionClient.deletedTask)
	require.Nil(t, svc.repoClient.removedTask)
	require.Empty(t, svc.taskRepo.deletedTaskID)
}

func TestRemoveAgentSession_KeepsTheAgentWhenItsPaneDoesNotClose(t *testing.T) {
	svc, _, codexID := newTwoAgentHarness(t)
	svc.sessionClient.closePaneErr = errors.New("tmux offline")

	err := svc.service.RemoveAgentSession(t.Context(), "task-1", codexID)

	require.ErrorContains(t, err, "close agent pane: tmux offline")
	for _, session := range svc.taskRepo.agentSessions {
		require.True(t, session.IsOpen(), session.ID)
	}
}

// An upgrade made the task's last conversation an agent session without a
// pane, after its Session was lost.
func TestRemoveAgentSession_EndsAnAgentSessionWithoutAPane(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.taskRepo.agentSessions = []AgentSession{{
		ID:                "agent-1",
		TaskID:            "task-1",
		Provider:          ProviderClaude,
		ProviderSessionID: "claude-old",
		StartedAt:         agentTestStart,
	}}
	svc.observation.now = func() time.Time { return removedAt }

	require.NoError(t, svc.service.RemoveAgentSession(t.Context(), "task-1", "agent-1"))

	require.Empty(t, svc.sessionClient.closedPanes)
	require.Equal(t, removedAt, svc.taskRepo.agentSessions[0].EndedAt)
}

func TestRemoveAgentSession_LeavesAnAgentSessionThatIsNotOpenAlone(t *testing.T) {
	svc, claudeID, codexID := newTwoAgentHarness(t)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionEnd, "codex-1", 102, "%2", 2), "")
	endedAt := svc.taskRepo.agentSessions[1].EndedAt
	require.False(t, endedAt.IsZero())

	// One already ended, one that never existed, and one of another task.
	require.NoError(t, svc.service.RemoveAgentSession(t.Context(), "task-1", codexID))
	require.NoError(t, svc.service.RemoveAgentSession(t.Context(), "task-1", "agent-unknown"))
	svc.taskRepo.listTasks = append(svc.taskRepo.listTasks, &Task{ID: "task-2", TmuxSession: "repo_other"})
	require.NoError(t, svc.service.RemoveAgentSession(t.Context(), "task-2", claudeID))

	require.Empty(t, svc.sessionClient.closedPanes)
	require.True(t, svc.taskRepo.agentSessions[0].IsOpen())
	require.Equal(t, endedAt, svc.taskRepo.agentSessions[1].EndedAt)
}

func TestRemoveAgentSession_FailsForAnUnknownTask(t *testing.T) {
	svc, claudeID, _ := newTwoAgentHarness(t)

	err := svc.service.RemoveAgentSession(t.Context(), "task-missing", claudeID)

	require.ErrorIs(t, err, ErrTaskNotFound)
	require.Empty(t, svc.sessionClient.closedPanes)
}
