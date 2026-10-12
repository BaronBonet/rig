package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// claudeAgent is an agent session that needs input in the task window.
func claudeAgent() core.LiveAgentSession {
	return core.LiveAgentSession{
		ID:           "agent-claude",
		Provider:     core.ProviderClaude,
		Pane:         core.TmuxPaneRef{Server: leadAgentPane.Server, ID: "%3"},
		Location:     &core.TmuxPaneLocation{WindowName: "task", WindowIndex: 0, PaneIndex: 0},
		Status:       core.AgentSessionStatus{Phase: core.TaskStatusPhaseWaitingForInput, RawEventName: "Stop"},
		LatestPrompt: "fix the flaky retry test",
	}
}

// codexAgent is an agent session working in a review window.
func codexAgent() core.LiveAgentSession {
	return core.LiveAgentSession{
		ID:           "agent-codex",
		Provider:     core.ProviderCodex,
		Pane:         core.TmuxPaneRef{Server: leadAgentPane.Server, ID: "%7"},
		Location:     &core.TmuxPaneLocation{WindowName: "review", WindowIndex: 2, PaneIndex: 1},
		Status:       core.AgentSessionStatus{Phase: core.TaskStatusPhaseWorking, RawEventName: "UserPromptSubmit"},
		LatestPrompt: "review the retry change",
	}
}

// agentsTaskModel is a loaded model with one Codex task, task-1, plus task-2
// below it in the same repo.
func agentsTaskModel(frontend *frontendHarness) model {
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoName:    "repo",
			DisplayName: "first task",
			TmuxSession: "repo_task_1",
			Provider:    core.ProviderCodex,
		},
		{
			ID:          "task-2",
			RepoName:    "repo",
			DisplayName: "second task",
			TmuxSession: "repo_task_2",
			Provider:    core.ProviderCodex,
		},
	}
	m := newLoadedModel(frontend)
	m.width = 96
	return m
}

// withAgentSessions feeds the model a status update for taskID listing
// sessions as its live agent sessions; the first one leads.
func withAgentSessions(t *testing.T, m model, taskID string, sessions ...core.LiveAgentSession) model {
	t.Helper()
	update := core.TaskStatusUpdate{
		TaskID:        taskID,
		Provider:      core.ProviderCodex,
		Phase:         core.TaskStatusPhaseStopped,
		AgentSessions: sessions,
	}
	if len(sessions) > 0 {
		update.LeadAgentSessionID = sessions[0].ID
		update.Provider = sessions[0].Provider
		update.Phase = sessions[0].Status.Phase
	}
	next, _ := m.Update(taskStatusUpdatedMsg{taskID: taskID, updates: make(chan core.TaskStatusUpdate), update: update})
	return asModel(t, next)
}

func pressKey(t *testing.T, m model, key tea.KeyPressMsg) model {
	t.Helper()
	next, _ := m.Update(key)
	return asModel(t, next)
}

// viewLineContaining returns the first view line containing text, without
// styling.
func viewLineContaining(t *testing.T, m model, text string) string {
	t.Helper()
	for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
		if strings.Contains(line, text) {
			return line
		}
	}
	t.Fatalf("no view line contains %q", text)
	return ""
}

func TestModel_ViewShowsAgentSubRowsOnlyForTwoOrMoreLiveAgents(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)

	single := withAgentSessions(t, m, "task-1", claudeAgent())
	require.NotContains(t, stripANSI(single.View().Content), "fix the flaky retry test")

	both := withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	// Each sub-row leads with its agent's status symbol.
	claudeLine := viewLineContaining(t, both, "fix the flaky retry test")
	require.Contains(t, claudeLine, "├ "+iconStatusNeedsInput+" claude")
	codexLine := viewLineContaining(t, both, "review the retry change")
	require.Contains(t, codexLine, "└ "+spinnerFrames[0]+" codex")

	// The sub-rows sit between their task and the next task.
	view := stripANSI(both.View().Content)
	require.Less(t, strings.Index(view, "first task"), strings.Index(view, "fix the flaky retry test"))
	require.Less(t, strings.Index(view, "review the retry change"), strings.Index(view, "second task"))
	for _, line := range strings.Split(both.View().Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), both.totalWidth(), stripANSI(line))
	}
}

func TestModel_AgentSubRowsNestUnderTheirTaskName(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())

	// Each sub-row's branch starts in the column of its task's name.
	nameColumn := statusColumn(t, viewLineContaining(t, m, "first task"), "first task")
	require.Equal(t, nameColumn, statusColumn(t, viewLineContaining(t, m, "fix the flaky retry test"), "├"))
	require.Equal(t, nameColumn, statusColumn(t, viewLineContaining(t, m, "review the retry change"), "└"))
}

func TestModel_AgentSubRowShowsWhereItRunsBeforeItsFirstPrompt(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	located := codexAgent()
	located.LatestPrompt = ""
	unlocated := claudeAgent()
	unlocated.LatestPrompt = ""
	unlocated.Location = nil

	unknown := codexAgent()
	unknown.ID = "agent-unknown"
	unknown.LatestPrompt = ""
	unknown.Location = nil
	unknown.Pane = core.TmuxPaneRef{}
	unknown.Status = core.AgentSessionStatus{}

	m = withAgentSessions(t, m, "task-1", unlocated, located, unknown)

	require.Contains(t, viewLineContaining(t, m, "window 2 (review) · pane 1"), "├ "+spinnerFrames[0]+" codex")
	require.Contains(t, viewLineContaining(t, m, "pane %3"), "├ "+iconStatusNeedsInput+" claude")
	// Without a status yet, it is starting.
	require.Contains(t, viewLineContaining(t, m, "no prompt yet"), "└ "+iconStatusStarting+" codex")
}

func TestModel_CursorStepsThroughAgentSubRows(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	down := tea.KeyPressMsg{Code: 'j', Text: "j"}
	up := tea.KeyPressMsg{Code: 'k', Text: "k"}

	type cursor struct {
		row   int
		agent string
	}
	at := func(m model) cursor { return cursor{row: m.selected, agent: m.selectedAgent} }

	require.Equal(t, cursor{row: 0}, at(m))
	m = pressKey(t, m, down)
	require.Equal(t, cursor{row: 0, agent: "agent-claude"}, at(m))
	require.True(t, strings.HasPrefix(viewLineContaining(t, m, "fix the flaky retry test"), "│"))
	require.False(t, strings.HasPrefix(viewLineContaining(t, m, "first task"), "│"))
	m = pressKey(t, m, down)
	require.Equal(t, cursor{row: 0, agent: "agent-codex"}, at(m))
	m = pressKey(t, m, down)
	require.Equal(t, cursor{row: 1}, at(m))
	m = pressKey(t, m, down)
	require.Equal(t, cursor{row: 1}, at(m))

	m = pressKey(t, m, up)
	require.Equal(t, cursor{row: 0, agent: "agent-codex"}, at(m))
	m = pressKey(t, m, up)
	m = pressKey(t, m, up)
	require.Equal(t, cursor{row: 0}, at(m))
	require.True(t, strings.HasPrefix(viewLineContaining(t, m, "first task"), "│"))

	// A page jump, or a jump to the top, lands on a task row.
	m = pressKey(t, m, down)
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	require.Equal(t, cursor{row: 0}, at(m))
	m = pressKey(t, m, down)
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.Equal(t, cursor{row: 0}, at(m))

	// The ends of the list are its first task row and its last line.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, cursor{row: 1}, at(m))
	secondClaude, secondCodex := claudeAgent(), codexAgent()
	secondClaude.ID, secondCodex.ID = "agent-claude-2", "agent-codex-2"
	m = withAgentSessions(t, m, "task-2", secondClaude, secondCodex)
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, cursor{row: 1, agent: "agent-codex-2"}, at(m))
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'g', Text: "g"})
	require.Equal(t, cursor{row: 0}, at(m))
}

func TestModel_TaskRowStaysHighlightedWhileOneOfItsAgentsIsSelected(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	for i := range m.rows {
		m.rows[i].task.CreatedAt = time.Now().Add(-15 * time.Minute)
	}
	taskLine := func(m model, index int) string { return m.renderRow(index, m.rows[index], m.totalWidth()) }

	// On the task row itself, its name and time stand out.
	selectedRow := taskLine(m, 0)
	require.True(t, strings.HasPrefix(stripANSI(selectedRow), "│"))
	require.Contains(t, selectedRow, lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("first task"))
	require.Contains(t, selectedRow, primaryStyle.Render("15m"))

	// On one of its agent sub-rows, the cursor's border moves to the sub-row,
	// and the task row keeps standing out from the rest.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	parentRow := taskLine(m, 0)
	require.False(t, strings.HasPrefix(stripANSI(parentRow), "│"))
	require.Contains(t, parentRow, lipgloss.NewStyle().Foreground(colorAccent).Render("first task"))
	require.Contains(t, parentRow, primaryStyle.Render("15m"))

	otherRow := taskLine(m, 1)
	require.Contains(t, otherRow, dimStyle.Render("second task"))
	require.Contains(t, otherRow, dimStyle.Render("15m"))
}

func TestModel_EnterOnAnAgentSubRowJumpsToItsPane(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	// Codex leads, but the user picks the Claude sub-row.
	m = withAgentSessions(t, m, "task-1", codexAgent(), claudeAgent())
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	msg := requireMsgType[taskOpenedMsg](t, runBatchCmd(t, cmd))
	next, _ = asModel(t, next).Update(msg)

	require.NoError(t, asModel(t, next).err)
	require.Equal(t, "task-1", frontend.attachedTask.ID)
	claudePane := claudeAgent().Pane
	require.Equal(t, []*core.TmuxPaneRef{&claudePane}, frontend.attachedPanes)
}

func TestModel_CleanupOnAnAgentSubRowRemovesOnlyThatAgent(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-codex", m.selectedAgent)
	require.Contains(t, stripANSI(m.listKeybindText()), "x remove")

	confirm := pressKey(t, m, tea.KeyPressMsg{Text: "x"})
	require.Equal(t, modeCleanupConfirm, confirm.mode)
	view := stripANSI(confirm.View().Content)
	require.Contains(t, view, "remove agent")
	require.Contains(t, view, "first task")
	require.Contains(t, view, "codex  review the retry change")
	require.Contains(t, view, "The agent's tmux pane will be closed")
	require.NotContains(t, view, "worktree will be deleted")

	next, cmd := confirm.Update(tea.KeyPressMsg{Text: "y"})
	require.NotNil(t, cmd)
	removing := asModel(t, next)
	require.Equal(t, modeBrowse, removing.mode)
	require.Nil(t, removing.cleanup.task)
	view = stripANSI(removing.View().Content)
	require.Contains(t, view, iconRemoving+" codex")
	removed, ok := runCmd(t, cmd).(agentSessionRemovedMsg)
	require.True(t, ok)
	require.Equal(t, []string{"task-1/agent-codex"}, frontend.removedAgents)
	require.Empty(t, frontend.deleteTaskIDs)

	// The agent being removed takes no action.
	require.Equal(t, modeBrowse, pressKey(t, removing, tea.KeyPressMsg{Text: "x"}).mode)

	// The reply can come before the status update without the agent, so the
	// sub-row stays marked until it goes.
	next, _ = removing.Update(removed)
	got := asModel(t, next)
	require.NoError(t, got.err)
	require.Len(t, got.rows, 2)
	require.Contains(t, stripANSI(got.View().Content), iconRemoving+" codex")

	// The task's status update without the agent drops its sub-row and moves
	// the cursor to the task row.
	got = withAgentSessions(t, got, "task-1", claudeAgent())
	require.Empty(t, got.selectedAgent)
	require.Equal(t, 0, got.selected)
}

func TestModel_CleanupKeepsTheAgentItOpenedOn(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	confirm := pressKey(t, m, tea.KeyPressMsg{Text: "x"})

	// The agent exits while the confirmation shows, so its sub-row goes and
	// the cursor falls back to the task row. Confirming still removes only
	// that agent, never the whole task.
	confirm = withAgentSessions(t, confirm, "task-1", codexAgent())
	require.Empty(t, confirm.selectedAgent)
	require.Contains(t, stripANSI(confirm.View().Content), "remove agent")

	_, cmd := confirm.Update(tea.KeyPressMsg{Text: "y"})
	_, ok := runCmd(t, cmd).(agentSessionRemovedMsg)
	require.True(t, ok)
	require.Equal(t, []string{"task-1/agent-claude"}, frontend.removedAgents)
	require.Empty(t, frontend.deleteTaskIDs)
}

func TestModel_CleanupOnATaskRowWithAgentsDeletesTheTask(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	require.Empty(t, m.selectedAgent)
	require.Contains(t, stripANSI(m.listKeybindText()), "x clean")

	confirm := pressKey(t, m, tea.KeyPressMsg{Text: "x"})
	require.Contains(t, stripANSI(confirm.View().Content), "The tmux session and worktree will be deleted.")

	next, cmd := confirm.Update(tea.KeyPressMsg{Text: "y"})
	_, ok := runCmd(t, cmd).(taskDeletedMsg)
	require.True(t, ok)
	require.True(t, asModel(t, next).deleting["task-1"])
	require.Equal(t, []string{"task-1"}, frontend.deleteTaskIDs)
	require.Empty(t, frontend.removedAgents)
}

func TestModel_AgentRemovalFailureShowsErrorAndKeepsTheAgent(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.removeAgentErr = errors.New("close agent pane: tmux offline")
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	confirm := pressKey(t, m, tea.KeyPressMsg{Text: "x"})

	next, cmd := confirm.Update(tea.KeyPressMsg{Text: "y"})
	removed, ok := runCmd(t, cmd).(agentSessionRemovedMsg)
	require.True(t, ok)
	next, _ = asModel(t, next).Update(removed)
	got := asModel(t, next)

	require.Empty(t, got.removingAgents)
	require.Equal(t, modeBrowse, got.mode)
	require.ErrorContains(t, got.err, "tmux offline")
	require.Equal(t, "agent-claude", got.selectedAgent)
	require.Len(t, agentSubRows(got.rows[0]), 2)
}

func TestModel_CancellingAgentRemovalForgetsItsTarget(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	confirm := pressKey(t, m, tea.KeyPressMsg{Text: "x"})

	got := pressKey(t, confirm, tea.KeyPressMsg{Text: "n"})
	require.Equal(t, modeBrowse, got.mode)
	require.Nil(t, got.cleanup.task)
	require.Nil(t, got.cleanup.agent)
	require.Empty(t, frontend.removedAgents)
}

func TestModel_ProviderCellNamesTheTasksOnlyAgent(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	// The task has no elapsed time or tokens, so its provider ends the row.
	provider := func(m model) string {
		line := stripANSI(m.renderRow(0, m.rows[0], m.totalWidth()))
		_, after, found := strings.Cut(line, "first task")
		require.True(t, found, line)
		return strings.TrimSpace(after)
	}

	require.Equal(t, "codex", provider(m))

	// A Codex task whose only agent is Claude shows Claude.
	claudeOnly := withAgentSessions(t, m, "task-1", claudeAgent())
	require.Equal(t, "claude", provider(claudeOnly))

	// With sub-rows, each names its own provider and the task row names none.
	mixed := withAgentSessions(t, m, "task-1", codexAgent(), claudeAgent())
	require.Empty(t, provider(mixed))
	view := stripANSI(mixed.View().Content)
	require.Contains(t, view, "├ "+spinnerFrames[0]+" codex ")
	require.Contains(t, view, "└ "+iconStatusNeedsInput+" claude ")

	twoCodex := codexAgent()
	twoCodex.ID = "agent-codex-2"
	sameProvider := withAgentSessions(t, m, "task-1", codexAgent(), twoCodex)
	require.Empty(t, provider(sameProvider))
}

func TestTaskListBlocks_SetsTasksWithSubRowsApart(t *testing.T) {
	m := model{rows: rowsFromTasks([]*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
		{ID: "task-2", RepoName: "repo-a", DisplayName: "second task", Provider: core.ProviderCodex},
		{ID: "task-3", RepoName: "repo-a", DisplayName: "third task", Provider: core.ProviderCodex},
		{ID: "task-4", RepoName: "repo-a", DisplayName: "fourth task", Provider: core.ProviderCodex},
		{ID: "task-5", RepoName: "repo-b", DisplayName: "fifth task", Provider: core.ProviderCodex},
	})}
	for _, index := range []int{1, 2, 4} {
		m.rows[index].status = &core.TaskStatusUpdate{
			Phase:         core.TaskStatusPhaseWorking,
			AgentSessions: []core.LiveAgentSession{claudeAgent(), codexAgent()},
		}
	}

	var lines []string
	for _, block := range m.taskListBlocks(100) {
		for _, line := range block.lines {
			lines = append(lines, strings.TrimSpace(strings.TrimLeft(stripANSI(line), "│ ")))
		}
	}

	// One blank line sets each task with sub-rows apart from its neighbours;
	// the repo header and the gap between repos already do at their edges.
	want := []string{
		"repo-a",
		"first task",
		"",
		"second task", "├ " + iconStatusNeedsInput + " claude", "└ " + spinnerFrames[0] + " codex",
		"",
		"third task", "├ " + iconStatusNeedsInput + " claude", "└ " + spinnerFrames[0] + " codex",
		"",
		"fourth task",
		"",
		"repo-b",
		"fifth task", "├ " + iconStatusNeedsInput + " claude", "└ " + spinnerFrames[0] + " codex",
	}
	require.Len(t, lines, len(want), strings.Join(lines, "\n"))
	for i, text := range want {
		if text == "" {
			require.Empty(t, lines[i], "line %d", i)
			continue
		}
		require.Contains(t, lines[i], text, "line %d", i)
	}
}

func TestModel_DetailViewDescribesTheSelectedAgentSession(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	background := claudeAgent()
	background.Status = core.AgentSessionStatus{
		Phase:          core.TaskStatusPhaseWorkingInBackground,
		RawEventName:   "Stop",
		BackgroundWork: core.TaskBackgroundWork{Subagents: 2, Shells: 1},
	}
	m = withAgentSessions(t, m, "task-1", codexAgent(), background)

	// On the task row, the detail panel describes the task, not an agent.
	require.NotContains(t, stripANSI(m.selectedTaskDetailView()), "pane")

	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	require.Equal(t, "window 0 (task) · pane 0", detailLines(m)[0])
}

func TestModel_CursorStaysOnItsAgentWhileSubRowsChange(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-codex", m.selectedAgent)

	// Another agent starts: the cursor stays on the Codex sub-row.
	another := claudeAgent()
	another.ID = "agent-claude-2"
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent(), another)
	require.Equal(t, 0, m.selected)
	require.Equal(t, "agent-codex", m.selectedAgent)
	require.True(t, strings.HasPrefix(viewLineContaining(t, m, "review the retry change"), "│"))

	// A reload of the task list keeps it there too.
	next, _ := m.Update(tasksLoadedMsg{tasks: frontend.listTasks})
	m = asModel(t, next)
	require.Equal(t, 0, m.selected)
	require.Equal(t, "agent-codex", m.selectedAgent)

	// The Codex agent ends: the cursor falls back to its task row.
	m = withAgentSessions(t, m, "task-1", claudeAgent(), another)
	require.Equal(t, 0, m.selected)
	require.Empty(t, m.selectedAgent)
	require.True(t, strings.HasPrefix(viewLineContaining(t, m, "first task"), "│"))

	// Down to one agent, the task shows no sub-rows to stay on.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	m = withAgentSessions(t, m, "task-1", claudeAgent())
	require.Equal(t, 0, m.selected)
	require.Empty(t, m.selectedAgent)
}

func TestModel_ViewKeepsTheSelectedAgentSubRowVisibleWhenItsTaskOverflows(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m.detailsHidden = true
	m.height = 8
	sessions := make([]core.LiveAgentSession, 0, 5)
	for i, prompt := range []string{"first prompt", "second prompt", "third prompt", "fourth prompt", "fifth prompt"} {
		session := codexAgent()
		session.ID = "agent-" + prompt
		session.Pane.ID = "%" + string(rune('1'+i))
		session.LatestPrompt = prompt
		sessions = append(sessions, session)
	}
	m = withAgentSessions(t, m, "task-1", sessions...)
	for range 5 {
		m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Equal(t, "agent-fifth prompt", m.selectedAgent)

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "fifth prompt")
	require.LessOrEqual(t, len(strings.Split(view, "\n")), m.height)
}

func TestModel_SingleAgentTaskRendersAsBeforeAgentSessions(t *testing.T) {
	status := core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "UserPromptSubmit",
	}
	update := func(m model, status core.TaskStatusUpdate) model {
		next, _ := m.Update(
			taskStatusUpdatedMsg{taskID: "task-1", updates: make(chan core.TaskStatusUpdate), update: status},
		)
		return asModel(t, next)
	}
	// Each model gets its own rows: models copied from one share them.
	withoutAgents := update(agentsTaskModel(newFrontendHarness()), status)
	status.LeadAgentSessionID = codexAgent().ID
	status.AgentSessions = []core.LiveAgentSession{codexAgent()}
	withOneAgent := update(agentsTaskModel(newFrontendHarness()), status)

	require.Equal(t, withoutAgents.View().Content, withOneAgent.View().Content)

	// Its one agent adds no stop for the cursor either: one key reaches the
	// next task.
	down := tea.KeyPressMsg{Code: 'j', Text: "j"}
	moved := pressKey(t, withOneAgent, down)
	require.True(t, strings.HasPrefix(viewLineContaining(t, moved, "second task"), "│"))
	require.Equal(t, pressKey(t, withoutAgents, down).View().Content, moved.View().Content)
}

// statusColumn is where text starts in a rendered line, in columns.
func statusColumn(t *testing.T, line string, text string) int {
	t.Helper()
	before, _, found := strings.Cut(line, text)
	require.True(t, found, "%q not in %q", text, line)
	return lipgloss.Width(before)
}

func TestModel_AgentSubRowTruncatesLongPromptsClearOfTheTaskColumns(t *testing.T) {
	// A wide character that would straddle the edge is left out whole, which
	// may end the prompt a cell sooner.
	for name, test := range map[string]struct {
		prompt    string
		wideSlack int
	}{
		"ascii":              {prompt: strings.Repeat("retry the flaky integration test ", 4)},
		"variation selector": {prompt: strings.Repeat("⚠️ fix the retry logic ✔️ ", 6), wideSlack: 1},
	} {
		t.Run(name, func(t *testing.T) {
			m := agentsTaskModel(newFrontendHarness())
			long := codexAgent()
			long.LatestPrompt = test.prompt
			m = withAgentSessions(t, m, "task-1", claudeAgent(), long)

			// Truncated, the prompt ends a gap short of the provider column of
			// the task rows.
			subRow := strings.TrimRight(viewLineContaining(t, m, "└ "+spinnerFrames[0]+" codex"), " ")
			require.True(t, strings.HasSuffix(subRow, "…"), subRow)
			providerColumn := statusColumn(t, viewLineContaining(t, m, "second task"), "codex")
			require.LessOrEqual(t, lipgloss.Width(subRow), providerColumn-len(nameGap))
			require.GreaterOrEqual(t, lipgloss.Width(subRow), providerColumn-len(nameGap)-test.wideSlack)
			for _, line := range strings.Split(m.View().Content, "\n") {
				require.LessOrEqual(t, lipgloss.Width(line), m.totalWidth(), stripANSI(line))
			}
		})
	}
}

func TestModel_AgentSubRowsFitNarrowTerminals(t *testing.T) {
	for width := 40; width <= 60; width++ {
		m := agentsTaskModel(newFrontendHarness())
		m.width = width
		m.detailsHidden = true
		m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())

		// The keybind header already overflows the narrowest widths; task
		// rows and sub-rows never do.
		for _, line := range strings.Split(m.View().Content, "\n")[1:] {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, stripANSI(line))
		}
		// A sub-row still nests under its task's name.
		require.Equal(
			t,
			statusColumn(t, viewLineContaining(t, m, "first task"), "first task"),
			statusColumn(t, viewLineContaining(t, m, "└"), "└"),
			"width %d", width,
		)
	}
}

func TestModel_DetailViewFitsTheSelectedAgentInEightyColumns(t *testing.T) {
	busy := claudeAgent()
	busy.Location = &core.TmuxPaneLocation{WindowName: "claude", WindowIndex: 3, PaneIndex: 1}
	busy.Status = core.AgentSessionStatus{
		Phase:          core.TaskStatusPhaseWorkingInBackground,
		RawEventName:   "Stop",
		BackgroundWork: core.TaskBackgroundWork{Subagents: 2, Shells: 1},
	}
	selectBusy := func(width int) model {
		m := agentsTaskModel(newFrontendHarness())
		m.width = width
		m = withAgentSessions(t, m, "task-1", codexAgent(), busy)
		m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
		m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
		require.Equal(t, "agent-claude", m.selectedAgent)
		return m
	}

	// The panel's lines fit the terminal, truncated where even a part is too
	// wide. The keybind header already overflows 72 columns.
	for _, width := range []int{72, 80} {
		m := selectBusy(width)
		for _, line := range strings.Split(m.selectedTaskDetailView(), "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, stripANSI(line))
		}
		require.Equal(t, "window 3 (claude) · pane 1", detailLines(m)[0], "width %d", width)
	}
}

func TestModel_TaskRowsProviderCellNamesItsOnlyAgentsProvider(t *testing.T) {
	// A Codex task whose only agent is Claude says Claude.
	m := withAgentSessions(t, agentsTaskModel(newFrontendHarness()), "task-1", claudeAgent())
	// The task has no elapsed time or tokens, so its provider ends its row.
	require.True(t, strings.HasSuffix(strings.TrimSpace(viewLineContaining(t, m, "first task")), " claude"))
}

func TestModel_PageDownAtTheEndNeverMovesTheCursorUp(t *testing.T) {
	m := agentsTaskModel(newFrontendHarness())
	m.height = 40
	secondClaude, secondCodex := claudeAgent(), codexAgent()
	secondClaude.ID, secondCodex.ID = "agent-claude-2", "agent-codex-2"
	m = withAgentSessions(t, m, "task-2", secondClaude, secondCodex)

	m = pressKey(t, m, tea.KeyPressMsg{Code: 'G', Text: "G"})
	require.Equal(t, "agent-codex-2", m.selectedAgent)
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	require.Equal(t, 1, m.selected)
	require.Equal(t, "agent-codex-2", m.selectedAgent)

	// From the last task's own row, a page down reaches the list's last line.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Empty(t, m.selectedAgent)
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	require.Equal(t, 1, m.selected)
	require.Equal(t, "agent-codex-2", m.selectedAgent)
}

func TestModel_PageKeysJumpWholeTasksPastSubRows(t *testing.T) {
	m := agentsTaskModel(newFrontendHarness())
	m.height = 40
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())

	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	require.Equal(t, 1, m.selected)
	require.Empty(t, m.selectedAgent)
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	require.Equal(t, 0, m.selected)
	require.Empty(t, m.selectedAgent)
}

func TestModel_AgentSubRowsKeepStartOrderWhicheverAgentLeads(t *testing.T) {
	m := agentsTaskModel(newFrontendHarness())
	next, _ := m.Update(taskStatusUpdatedMsg{
		taskID:  "task-1",
		updates: make(chan core.TaskStatusUpdate),
		update: core.TaskStatusUpdate{
			TaskID:             "task-1",
			Provider:           core.ProviderCodex,
			Phase:              core.TaskStatusPhaseWorking,
			LeadAgentSessionID: codexAgent().ID,
			AgentSessions:      []core.LiveAgentSession{claudeAgent(), codexAgent()},
		},
	})
	view := stripANSI(asModel(t, next).View().Content)

	require.Less(
		t,
		strings.Index(view, "├ "+iconStatusNeedsInput+" claude"),
		strings.Index(view, "└ "+spinnerFrames[0]+" codex"),
	)
}

func TestModel_ReloadThatMovesTheTaskKeepsTheAgentCursor(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-2", claudeAgent(), codexAgent())
	for range 3 {
		m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	require.Equal(t, 1, m.selected)
	require.Equal(t, "agent-codex", m.selectedAgent)

	// Another client created a task that sorts first.
	zeroth := &core.Task{
		ID: "task-0", RepoName: "repo", DisplayName: "zeroth task", TmuxSession: "repo_task_0",
		Provider: core.ProviderCodex,
	}
	next, _ := m.Update(tasksLoadedMsg{tasks: append([]*core.Task{zeroth}, frontend.listTasks...)})
	m = asModel(t, next)

	require.Equal(t, "task-2", m.rows[m.selected].task.ID)
	require.Equal(t, "agent-codex", m.selectedAgent)
}

func TestModel_AgentSubRowShowsAToolCallLikeItsTask(t *testing.T) {
	m := agentsTaskModel(newFrontendHarness())
	running := codexAgent()
	running.Status = core.AgentSessionStatus{Phase: core.TaskStatusPhaseWorking, RawEventName: "PreToolUse"}
	m = withAgentSessions(t, m, "task-1", claudeAgent(), running)

	// The sub-row spins like any working row.
	require.Contains(t, viewLineContaining(t, m, "review the retry change"), "└ "+spinnerFrames[0]+" codex")
}

func TestModel_AgentTextNeverReachesTheTerminalRaw(t *testing.T) {
	m := agentsTaskModel(newFrontendHarness())
	noisy := codexAgent()
	noisy.LatestPrompt = "first line\nsecond \x1b[31mred\x1b[0m line\r\n\tthird\x07"
	m = withAgentSessions(t, m, "task-1", claudeAgent(), noisy)
	next, _ := m.Update(taskActivityLoadedMsg{
		taskID: "task-1",
		activity: []core.TaskActivityEvent{
			{Role: core.TaskActivityRoleUser, Text: "run the \x1b[1;32mgreen\x1b[0m suite"},
			{Role: core.TaskActivityRoleAssistant, Text: "done \x1b]0;title\x07 all \x1b[2Kpassing"},
		},
	})
	m = asModel(t, next)

	view := m.View().Content
	for _, raw := range []string{"\x1b[31m", "\x1b[1;32m", "\x1b]0;", "\x1b[2K", "\x07", "\r", "\t"} {
		require.NotContains(t, view, raw)
	}
	// A prompt spanning lines shows on one sub-row line.
	require.Contains(t, viewLineContaining(t, m, "└ "+spinnerFrames[0]+" codex"), "first line second red line third")
	plain := stripANSI(view)
	require.Contains(t, plain, "run the green suite")
	require.Contains(t, plain, "done all passing")
}

// withUsage loads a task's compactions into the model as the frontend
// reports them.
func withUsage(t *testing.T, m model, taskID string, compactions map[string]core.Compactions) model {
	t.Helper()
	return withTaskUsage(t, m, taskID, &core.TaskUsage{AgentCompactions: compactions})
}

// withTaskUsage loads a task's usage into the model as the frontend reports
// it.
func withTaskUsage(t *testing.T, m model, taskID string, usage *core.TaskUsage) model {
	t.Helper()
	next, _ := m.Update(taskUsageLoadedMsg{taskID: taskID, usage: usage})
	return asModel(t, next)
}

// contextAgentsModel is compactedAgentsModel with each agent's context
// usage: task-1's Claude agent is 64% full, its Codex agent 9%, and task-2's
// single agent 41%.
func contextAgentsModel(t *testing.T, width int) model {
	t.Helper()
	m := compactedAgentsModel(t, width)
	m = withTaskUsage(t, m, "task-1", &core.TaskUsage{
		AgentCompactions: map[string]core.Compactions{"agent-codex": {Total: 2}},
		AgentContext: map[string]core.ContextUsage{
			"agent-claude": {Tokens: 128_000, Window: 200_000},
			"agent-codex":  {Tokens: 24_500, Window: 258_400},
		},
	})
	return withTaskUsage(t, m, "task-2", &core.TaskUsage{
		AgentCompactions: map[string]core.Compactions{"agent-sole": {Total: 1, Manual: 1}},
		AgentContext:     map[string]core.ContextUsage{"agent-sole": {Tokens: 106_000, Window: 258_400}},
	})
}

func TestModel_AgentSubRowsShowTheirContextInTheTaskRowsColumn(t *testing.T) {
	m := contextAgentsModel(t, 96)

	// A task with sub-rows leaves its agents' context to them.
	require.NotContains(t, viewLineContaining(t, m, "first task"), "%")
	claudeLine := viewLineContaining(t, m, "fix the flaky retry test")
	require.Contains(t, claudeLine, "64%")
	require.NotContains(t, claudeLine, iconCompacted)
	codexLine := viewLineContaining(t, m, "review the retry change")
	require.Contains(t, codexLine, "9% "+iconCompacted+"2")

	// Right-aligned, each percentage ends in the single-agent task row's
	// context column, and the markers after it line up too.
	taskLine := viewLineContaining(t, m, "second task")
	require.Contains(t, taskLine, "41% "+iconCompacted+"1")
	for _, subRow := range []string{claudeLine, codexLine} {
		require.Equal(t, statusColumn(t, taskLine, "%"), statusColumn(t, subRow, "%"), subRow)
	}
	require.Equal(t, statusColumn(t, taskLine, iconCompacted), statusColumn(t, codexLine, iconCompacted))

	// Like its compaction marker, a sub-row's percentage stands out on the
	// selected line.
	usage := agentRowUsage{context: &core.ContextUsage{Tokens: 24_500, Window: 258_400}}
	require.Contains(t, m.renderAgentSubRow(codexAgent(), usage, true, false, 96), mutedStyle.Render("9%"))
	require.Contains(t, m.renderAgentSubRow(codexAgent(), usage, true, true, 96), primaryStyle.Render("9%"))
}

func TestModel_AgentSubRowWithoutAKnownWindowLeavesItsContextBlank(t *testing.T) {
	m := contextAgentsModel(t, 96)
	m = withTaskUsage(t, m, "task-1", &core.TaskUsage{
		AgentCompactions: map[string]core.Compactions{"agent-codex": {Total: 2}},
		AgentContext:     map[string]core.ContextUsage{"agent-codex": {Tokens: 24_500}},
	})

	require.NotContains(t, viewLineContaining(t, m, "fix the flaky retry test"), "%")
	codexLine := viewLineContaining(t, m, "review the retry change")
	require.NotContains(t, codexLine, "%")
	// Its compaction marker keeps its column.
	require.Equal(
		t,
		statusColumn(t, viewLineContaining(t, m, "second task"), iconCompacted),
		statusColumn(t, codexLine, iconCompacted),
	)
}

func TestModel_ContextMarkersFitEveryWidth(t *testing.T) {
	for width := 40; width <= 120; width++ {
		m := contextAgentsModel(t, width)
		m.detailsHidden = true
		layout := taskRowLayoutFor(width)

		// The keybind header already overflows the narrowest widths; task rows
		// and sub-rows stay on one line and never do.
		lines := strings.Split(m.View().Content, "\n")
		require.Len(t, lines, 8, "width %d", width)
		for _, line := range lines[1:] {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, stripANSI(line))
		}

		// The context column goes first, and sub-rows follow the task rows.
		view := stripANSI(m.View().Content)
		require.Equal(t, layout.context, strings.Contains(view, "%"), "width %d", width)
		require.Equal(t, layout.compactions, strings.Contains(view, iconCompacted), "width %d", width)
		taskLine := viewLineContaining(t, m, "second task")
		if layout.context {
			for _, subRow := range []string{"├", "└"} {
				require.Equal(
					t,
					statusColumn(t, taskLine, "%"),
					statusColumn(t, viewLineContaining(t, m, subRow), "%"),
					"width %d", width,
				)
			}
		}
		if layout.compactions {
			require.Equal(
				t,
				statusColumn(t, taskLine, iconCompacted),
				statusColumn(t, viewLineContaining(t, m, "└"), iconCompacted),
				"width %d", width,
			)
		}
	}
}

// detailContext is the context part of the detail panel's summary, without
// its label.
func detailContext(t *testing.T, m model) string {
	t.Helper()
	_, value, found := strings.Cut(detailLines(m)[0], "context ")
	require.True(t, found, "no context in %q", detailLines(m)[0])
	value, _, _ = strings.Cut(value, detailSummaryGap)
	return value
}

func TestModel_DetailViewShowsTheAgentsContext(t *testing.T) {
	m := contextAgentsModel(t, 96)

	// On a task with sub-rows, each agent's context is left to it.
	require.NotContains(t, stripANSI(m.View().Content), "context")

	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	require.Equal(t, "64% · 128.0k of 200.0k", detailContext(t, m))

	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-codex", m.selectedAgent)
	require.Equal(t, "9% · 24.5k of 258.4k", detailContext(t, m))
	// The context comes before the compactions.
	view := stripANSI(m.View().Content)
	require.Less(t, strings.Index(view, "context "), strings.Index(view, "compacted 2×"))

	// A task with one agent shows that agent's context.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, m.selected)
	require.Equal(t, "41% · 106.0k of 258.4k", detailContext(t, m))

	// Without a known window, it shows only the tokens, on the task row and
	// on a sub-row alike.
	m = withTaskUsage(t, m, "task-1", &core.TaskUsage{
		AgentContext: map[string]core.ContextUsage{"agent-codex": {Tokens: 24_500}},
	})
	m = withTaskUsage(t, m, "task-2", &core.TaskUsage{
		AgentContext: map[string]core.ContextUsage{"agent-sole": {Tokens: 106_000}},
	})
	require.Equal(t, "106.0k", detailContext(t, m))
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, "agent-codex", m.selectedAgent)
	require.Equal(t, "24.5k", detailContext(t, m))

	// An agent without a recorded context has no context line.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'k', Text: "k"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	require.NotContains(t, stripANSI(m.View().Content), "context")
}

func TestContextDetailText(t *testing.T) {
	for _, tc := range []struct {
		context core.ContextUsage
		want    string
	}{
		{core.ContextUsage{Tokens: 128_000, Window: 200_000}, "64% · 128.0k of 200.0k"},
		{core.ContextUsage{Tokens: 128_000, Window: 1_000_000}, "12% · 128.0k of 1.0m"},
		{core.ContextUsage{Tokens: 300_000, Window: 258_400}, "100% · 300.0k of 258.4k"},
		{core.ContextUsage{Tokens: 128_000}, "128.0k"},
		{core.ContextUsage{Tokens: 950}, "950"},
	} {
		require.Equal(t, tc.want, contextDetailText(tc.context), "%+v", tc.context)
	}
}

// compactedAgentsModel has task-1 with a Claude and a Codex agent, of which
// Codex compacted three times, and task-2 with a single agent that compacted
// once.
func compactedAgentsModel(t *testing.T, width int) model {
	t.Helper()
	m := agentsTaskModel(newFrontendHarness())
	m.width = width
	soleAgent := codexAgent()
	soleAgent.ID = "agent-sole"
	soleAgent.LatestPrompt = "tidy the docs"
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	m = withAgentSessions(t, m, "task-2", soleAgent)
	m = withUsage(t, m, "task-1", map[string]core.Compactions{"agent-codex": {Total: 3}})
	return withUsage(t, m, "task-2", map[string]core.Compactions{"agent-sole": {Total: 1, Manual: 1}})
}

func TestModel_AgentSubRowShowsItsCompactionsInTheTaskRowsColumn(t *testing.T) {
	m := compactedAgentsModel(t, 96)

	// A task with sub-rows leaves compactions to the agent that compacted.
	require.NotContains(t, viewLineContaining(t, m, "first task"), iconCompacted)
	require.NotContains(t, viewLineContaining(t, m, "fix the flaky retry test"), iconCompacted)
	codexLine := viewLineContaining(t, m, "review the retry change")
	require.Contains(t, codexLine, iconCompacted+"3")

	// The sub-row's marker lines up with a single-agent task row's.
	require.Equal(
		t,
		statusColumn(t, viewLineContaining(t, m, "second task"), iconCompacted+"1"),
		statusColumn(t, codexLine, iconCompacted+"3"),
	)

	// Like the task row's time, the marker stands out on the selected line.
	require.Contains(
		t,
		m.renderAgentSubRow(codexAgent(), agentRowUsage{compactions: core.Compactions{Total: 3}}, true, false, 96),
		mutedStyle.Render(iconCompacted+"3"),
	)
	require.Contains(
		t,
		m.renderAgentSubRow(codexAgent(), agentRowUsage{compactions: core.Compactions{Total: 3}}, true, true, 96),
		primaryStyle.Render(iconCompacted+"3"),
	)
	require.Contains(t, model{selected: 0}.renderRow(0, compactedRow(), 96), primaryStyle.Render(iconCompacted+"2"))
	require.Contains(t, model{selected: 1}.renderRow(0, compactedRow(), 96), mutedStyle.Render(iconCompacted+"2"))
}

func TestRenderCompactionsCell_FitsLargeCountsInItsColumn(t *testing.T) {
	for total, want := range map[int]string{0: "    ", 7: "↻7  ", 99: "↻99 ", 100: "↻99+", 4321: "↻99+"} {
		cell := stripANSI(renderCompactionsCell(core.Compactions{Total: total}, mutedStyle))
		require.Equal(t, want, cell, "total %d", total)
	}
}

func TestModel_CompactionMarkersFitEveryWidth(t *testing.T) {
	for width := 40; width <= 120; width++ {
		m := compactedAgentsModel(t, width)
		m.detailsHidden = true

		for _, line := range strings.Split(m.View().Content, "\n")[1:] {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, stripANSI(line))
		}
		if !taskRowLayoutFor(width).compactions {
			require.NotContains(t, stripANSI(m.View().Content), iconCompacted, "width %d", width)
			continue
		}
		require.Equal(
			t,
			statusColumn(t, viewLineContaining(t, m, "second task"), iconCompacted),
			statusColumn(t, viewLineContaining(t, m, "└"), iconCompacted),
			"width %d", width,
		)
	}
}

func TestModel_DetailViewShowsTheAgentsCompactions(t *testing.T) {
	m := compactedAgentsModel(t, 96)
	m = withUsage(t, m, "task-1", map[string]core.Compactions{
		"agent-claude": {Total: 3, Auto: 2, Manual: 1},
		"agent-codex":  {Total: 2},
	})

	// On a task with sub-rows, each agent's compactions are left to it.
	require.NotContains(t, stripANSI(m.View().Content), "compacted")

	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	require.Contains(t, stripANSI(m.View().Content), "compacted 3× (2 auto, 1 manual)")

	// Codex does not record what triggered a compaction.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-codex", m.selectedAgent)
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "compacted 2×")
	require.NotContains(t, view, "2× (")

	// A task with one agent shows that agent's compactions.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, m.selected)
	require.Contains(t, stripANSI(m.View().Content), "compacted 1× (manual)")
}

func TestCompactionsDetailText(t *testing.T) {
	for _, tc := range []struct {
		compactions core.Compactions
		want        string
	}{
		{core.Compactions{Total: 2}, "2×"},
		{core.Compactions{Total: 2, Auto: 2}, "2× (auto)"},
		{core.Compactions{Total: 1, Manual: 1}, "1× (manual)"},
		{core.Compactions{Total: 3, Auto: 2, Manual: 1}, "3× (2 auto, 1 manual)"},
		{core.Compactions{Total: 3, Auto: 1}, "3× (1 auto)"},
	} {
		require.Equal(t, tc.want, compactionsDetailText(tc.compactions), "%+v", tc.compactions)
	}
}

func TestModel_AgentSubRowShowsItsNameOnceNamed(t *testing.T) {
	m := agentsTaskModel(newFrontendHarness())
	named := codexAgent()
	named.Name = "GH #124 Task CLI"
	m = withAgentSessions(t, m, "task-1", claudeAgent(), named)

	require.Contains(t, viewLineContaining(t, m, "GH #124 Task CLI"), "└ "+spinnerFrames[0]+" codex")
	require.NotContains(t, stripANSI(m.View().Content), "review the retry change")
	// Until it is named, an agent shows its latest prompt.
	require.Contains(t, viewLineContaining(t, m, "fix the flaky retry test"), "├ "+iconStatusNeedsInput+" claude")
}

func TestModel_DetailViewShowsTheSelectedAgentsOwnActivity(t *testing.T) {
	m := agentsTaskModel(newFrontendHarness())
	m.width = 120
	claude := claudeAgent()
	claude.Name = "Retry test fix"
	codex := codexAgent()
	codex.Name = "Retry change review"
	m = withAgentSessions(t, m, "task-1", claude, codex)
	next, _ := m.Update(taskActivityLoadedMsg{
		taskID: "task-1",
		activity: []core.TaskActivityEvent{
			{AgentSessionID: "agent-claude", Role: core.TaskActivityRoleAssistant, Text: "claude reply"},
			{AgentSessionID: "agent-codex", Role: core.TaskActivityRoleUser, Text: "review the retry change"},
			{AgentSessionID: "agent-codex", Role: core.TaskActivityRoleAssistant, Text: "codex reply"},
		},
	})
	m = asModel(t, next)

	// On the task row, the activity is the task's.
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "review the retry change")
	require.Contains(t, view, "codex reply")

	// On an agent's sub-row, it is that agent's own. Its latest prompt shows
	// even when the activity loaded has scrolled past it.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	view = stripANSI(m.View().Content)
	require.Contains(t, view, "fix the flaky retry test")
	require.Contains(t, view, "claude reply")
	require.NotContains(t, view, "review the retry change")
	require.NotContains(t, view, "codex reply")
}
