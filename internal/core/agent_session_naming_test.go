package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// newAgentNamingHarness returns an agent session harness whose task-1 was
// created from a prompt, with a Claude agent in pane %1 and a Codex agent in
// pane %2, and whose status cycles run on demand and name agents before they
// return.
func newAgentNamingHarness(t *testing.T) (*testTaskServiceHarness, *taskStatusObserver) {
	t.Helper()
	svc := newAgentSessionHarness(t)
	svc.taskRepo.listTasks[0].RepoRoot = "/tmp/repo"
	svc.taskRepo.listTasks[0].Prompt = "add retries to the sync client"
	svc.taskRepo.listTasks[0].DisplayName = "Sync client retries"
	svc.agentPanes("claude", "codex")
	svc.observation.startBackground = func(work func()) { work() }
	observer := svc.manualStatusObserver()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	return svc, observer
}

// agentNames maps each open agent session's pane to its name.
func (h *testTaskServiceHarness) agentNames() map[string]string {
	h.taskRepo.mu.Lock()
	defer h.taskRepo.mu.Unlock()
	names := make(map[string]string)
	for _, session := range h.taskRepo.agentSessions {
		if session.IsOpen() {
			names[session.TmuxPane] = session.Name
		}
	}
	return names
}

// startBothAgents sends the Claude agent the task's prompt, with more added,
// and the Codex agent a prompt of its own, each followed by a reply.
func (h *testTaskServiceHarness) startBothAgents(t *testing.T) {
	t.Helper()
	h.handleAgentHook(t, promptHook(ProviderClaude, "sess-claude", 101, "%1", 0,
		"add retries to the sync client.\nKeep the backoff short"), TaskStatusPhaseWorking)
	h.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex", 102, "%2", 1,
		"implement gh issue 124"), TaskStatusPhaseWorking)
	h.handleAgentHook(t, promptHook(ProviderClaude, "sess-claude", 101, "%1", 2, "yes"), TaskStatusPhaseWorking)
	h.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex", 102, "%2", 3, "looks good"), TaskStatusPhaseWorking)
}

func TestAgentNaming_EachAgentIsNamedFromTheFirstPromptOfItsConversation(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.providerRepo.suggestedSuggestion = TaskSuggestion{Name: "Task CLI", Ticket: "GH #124", BranchType: "feat"}
	svc.startBothAgents(t)

	observer.runCycle()

	// The agent Rig launched with the task's prompt takes the task's name; the
	// other is named by its own provider, in the task's repository.
	require.Equal(t, map[string]string{"%1": "Sync client retries", "%2": "GH #124 Task CLI"}, svc.agentNames())
	require.Empty(t, svc.claudeRepo.suggestPrompts)
	require.Equal(t, []string{"implement gh issue 124"}, svc.providerRepo.suggestPrompts)
	require.Equal(t, "/tmp/repo", svc.providerRepo.suggestRepoRoot)

	agents := svc.latestTaskStatus(t).AgentSessions
	require.Len(t, agents, 2)
	require.Equal(t, "Sync client retries", agents[0].Name)
	require.Equal(t, "yes", agents[0].LatestPrompt)
	require.Equal(t, "GH #124 Task CLI", agents[1].Name)

	// A named agent is not named again.
	observer.runCycle()
	require.Len(t, svc.providerRepo.suggestPrompts, 1)
}

func TestAgentNaming_ATaskWithOneAgentLeavesItUnnamed(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex", 102, "%2", 0, "implement gh issue 124"),
		TaskStatusPhaseWorking)

	observer.runCycle()

	require.Equal(t, map[string]string{"%2": ""}, svc.agentNames())
	require.Empty(t, svc.providerRepo.suggestPrompts)
}

func TestAgentNaming_AnAgentWithoutAPromptWaitsForItsFirst(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-claude", 101, "%1", 0, "add retries to the sync client"),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "sess-codex", 102, "%2", 1),
		TaskStatusPhaseStarting)

	observer.runCycle()
	require.Equal(t, map[string]string{"%1": "Sync client retries", "%2": ""}, svc.agentNames())

	svc.providerRepo.suggestedName = "Review the retry change"
	svc.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex", 102, "%2", 2, "review the retry change"),
		TaskStatusPhaseWorking)
	observer.runCycle()
	require.Equal(t, "Review the retry change", svc.agentNames()["%2"])
}

func TestAgentNaming_AConversationStartedAfreshRenamesTheAgent(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.providerRepo.suggestedName = "Task CLI"
	svc.startBothAgents(t)
	observer.runCycle()

	// /new starts a conversation afresh. Until its first prompt, the agent
	// keeps its name.
	newStart := agentHook(ProviderCodex, HookEventSessionStart, "sess-codex-2", 102, "%2", 5)
	newStart.StartSource = "startup"
	svc.handleAgentHook(t, newStart, TaskStatusPhaseStarting)
	observer.runCycle()
	require.Equal(t, "Task CLI", svc.agentNames()["%2"])

	svc.providerRepo.suggestedName = "Flaky socket test"
	svc.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex-2", 102, "%2", 6, "fix the flaky socket test"),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex-2", 102, "%2", 7, "yes"), TaskStatusPhaseWorking)
	observer.runCycle()

	require.Equal(t, "Flaky socket test", svc.agentNames()["%2"])
	require.Equal(t, []string{"implement gh issue 124", "fix the flaky socket test"}, svc.providerRepo.suggestPrompts)
}

func TestAgentNaming_AClaudeClearRenamesTheAgent(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.startBothAgents(t)
	observer.runCycle()

	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-claude", 101, "%1", 5, "clear"), "")
	clearStart := agentHook(ProviderClaude, HookEventSessionStart, "sess-claude-2", 101, "%1", 5)
	clearStart.StartSource = "clear"
	svc.handleAgentHook(t, clearStart, TaskStatusPhaseStarting)
	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-claude-2", 101, "%1", 6, "document the retries"),
		TaskStatusPhaseWorking)

	// The old name no longer says what the agent works on, so until the new
	// conversation is named, the agent shows its prompt.
	agents := svc.latestTaskStatus(t).AgentSessions
	require.Empty(t, agents[0].Name)
	require.Equal(t, "document the retries", agents[0].LatestPrompt)

	svc.claudeRepo.suggestedName = "Docs for retries"
	observer.runCycle()

	require.Equal(t, "Docs for retries", svc.agentNames()["%1"])
	require.Equal(t, []string{"document the retries"}, svc.claudeRepo.suggestPrompts)
	require.Equal(t, "Docs for retries", svc.latestTaskStatus(t).AgentSessions[0].Name)
}

func TestAgentNaming_AFailedRenameShowsThePromptRatherThanTheOldName(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.providerRepo.suggestedName = "Task CLI"
	svc.startBothAgents(t)
	observer.runCycle()

	newStart := agentHook(ProviderCodex, HookEventSessionStart, "sess-codex-2", 102, "%2", 5)
	newStart.StartSource = "clear"
	svc.handleAgentHook(t, newStart, TaskStatusPhaseStarting)
	svc.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex-2", 102, "%2", 6, "fix the flaky socket test"),
		TaskStatusPhaseWorking)
	svc.providerRepo.suggestErr = errors.New("codex print mode failed")
	observer.runCycle()

	agents := svc.latestTaskStatus(t).AgentSessions
	require.Empty(t, agents[1].Name)
	require.Equal(t, "fix the flaky socket test", agents[1].LatestPrompt)
}

func TestAgentNaming_AResumedConversationKeepsTheName(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.providerRepo.suggestedName = "Task CLI"
	svc.startBothAgents(t)
	observer.runCycle()

	resume := agentHook(ProviderCodex, HookEventSessionStart, "sess-codex-old", 102, "%2", 5)
	resume.StartSource = "resume"
	svc.handleAgentHook(t, resume, TaskStatusPhaseStarting)
	svc.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex-old", 102, "%2", 6, "carry on"),
		TaskStatusPhaseWorking)
	observer.runCycle()

	require.Equal(t, "Task CLI", svc.agentNames()["%2"])
	require.Len(t, svc.providerRepo.suggestPrompts, 1)
}

func TestAgentNaming_AForkKeepsTheName(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.claudeRepo.suggestedName = "Docs for retries"
	svc.taskRepo.listTasks[0].Prompt = "something else entirely"
	svc.providerRepo.suggestedName = "Task CLI"
	svc.startBothAgents(t)
	observer.runCycle()

	// Claude reports a fork with its SessionStart. A fork Codex reports no
	// SessionStart for moves the agent with its first prompt.
	fork := agentHook(ProviderClaude, HookEventSessionStart, "sess-claude-fork", 101, "%1", 5)
	fork.StartSource = "fork"
	svc.handleAgentHook(t, fork, TaskStatusPhaseStarting)
	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-claude-fork", 101, "%1", 6, "try the other way"),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, promptHook(ProviderCodex, "sess-codex-fork", 102, "%2", 6, "try plan B"),
		TaskStatusPhaseWorking)
	observer.runCycle()

	require.Equal(t, map[string]string{"%1": "Docs for retries", "%2": "Task CLI"}, svc.agentNames())
	require.Len(t, svc.claudeRepo.suggestPrompts, 1)
	require.Len(t, svc.providerRepo.suggestPrompts, 1)
	agents := svc.latestTaskStatus(t).AgentSessions
	require.Equal(t, "Docs for retries", agents[0].Name)
	require.Equal(t, "Task CLI", agents[1].Name)
}

func TestAgentNaming_AgentsOfALostSessionAreNotNamed(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.startBothAgents(t)
	// The task's Session is gone: its agents wait for Reconnect, and the task
	// shows no sub-rows.
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locateTaskSessionPanes = map[string][]string{}
	svc.sessionClient.mu.Unlock()

	observer.runCycle()

	require.Equal(t, map[string]string{"%1": "", "%2": ""}, svc.agentNames())
	require.Empty(t, svc.providerRepo.suggestPrompts)
}

func TestTaskNameForPrompt(t *testing.T) {
	task := &Task{Prompt: "fix  the\nretries", DisplayName: "Retry fix"}
	for prompt, want := range map[string]string{
		"fix the retries":                  "Retry fix",
		"fix the retries. Keep it short":   "Retry fix",
		"fix the\tretries\n\nand the docs": "Retry fix",
		"fix the retriesmith":              "",
		"fix the retries2":                 "",
		"please fix the retries":           "",
		"":                                 "",
	} {
		require.Equal(t, want, taskNameForPrompt(task, prompt), prompt)
	}
	require.Empty(t, taskNameForPrompt(&Task{DisplayName: "Unprompted"}, "anything"))
}

func TestAgentNaming_AFailedNamingIsNotRetriedEachCycle(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	svc.providerRepo.suggestErr = errors.New("codex print mode failed")
	svc.startBothAgents(t)

	observer.runCycle()
	observer.runCycle()

	require.Empty(t, svc.agentNames()["%2"])
	require.Len(t, svc.providerRepo.suggestPrompts, 1)
	// The agent goes on showing its latest prompt.
	require.Equal(t, "looks good", svc.latestTaskStatus(t).AgentSessions[1].LatestPrompt)
}

func TestAgentNaming_ANameForAConversationTheAgentLeftIsDropped(t *testing.T) {
	svc, observer := newAgentNamingHarness(t)
	var naming []func()
	svc.observation.startBackground = func(work func()) { naming = append(naming, work) }
	svc.providerRepo.suggestedName = "Task CLI"
	svc.startBothAgents(t)
	observer.runCycle()
	require.Len(t, naming, 2)

	// The Codex agent starts a new conversation while it is being named.
	newStart := agentHook(ProviderCodex, HookEventSessionStart, "sess-codex-2", 102, "%2", 5)
	newStart.StartSource = "clear"
	svc.handleAgentHook(t, newStart, TaskStatusPhaseStarting)
	for _, work := range naming {
		work()
	}

	require.Equal(t, map[string]string{"%1": "Sync client retries", "%2": ""}, svc.agentNames())
}
