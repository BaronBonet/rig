package tui

import (
	"strings"
	"testing"

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
	claudeLine := viewLineContaining(t, both, "fix the flaky retry test")
	require.Contains(t, claudeLine, "├ claude")
	require.Contains(t, claudeLine, "◐ needs input")
	codexLine := viewLineContaining(t, both, "review the retry change")
	require.Contains(t, codexLine, "└ codex")
	require.Contains(t, codexLine, "● working")

	// The sub-rows sit between their task and the next task.
	view := stripANSI(both.View().Content)
	require.Less(t, strings.Index(view, "first task"), strings.Index(view, "fix the flaky retry test"))
	require.Less(t, strings.Index(view, "review the retry change"), strings.Index(view, "second task"))
	for _, line := range strings.Split(both.View().Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), both.totalWidth(), stripANSI(line))
	}
}

func TestModel_AgentSubRowStatusLinesUpWithTheTaskStatusColumn(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())

	taskLine := viewLineContaining(t, m, "first task")
	subRowLine := viewLineContaining(t, m, "review the retry change")

	require.Equal(
		t,
		lipgloss.Width(strings.SplitN(taskLine, "◐ needs input", 2)[0]),
		lipgloss.Width(strings.SplitN(subRowLine, "● working", 2)[0]),
	)
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

	require.Contains(t, viewLineContaining(t, m, "window 2 (review) · pane 1"), "├ codex")
	require.Contains(t, viewLineContaining(t, m, "pane %3"), "├ claude")
	unknownLine := viewLineContaining(t, m, "no prompt yet")
	require.Contains(t, unknownLine, "└ codex")
	// Without a status yet, it is starting.
	require.Contains(t, unknownLine, "◐ starting")
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

func TestModel_TaskActionsOnAnAgentSubRowActOnItsTask(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)

	confirm := pressKey(t, m, tea.KeyPressMsg{Text: "x"})
	require.Equal(t, modeCleanupConfirm, confirm.mode)
	require.Contains(t, stripANSI(confirm.View().Content), "first task")

	next, cmd := confirm.Update(tea.KeyPressMsg{Text: "y"})
	require.NotNil(t, cmd)
	requireMsgType[taskDeletedMsg](t, runBatchCmd(t, cmd))
	require.Equal(t, opDeleting, asModel(t, next).pending)
	require.Equal(t, []string{"task-1"}, frontend.deleteTaskIDs)
}

func TestModel_ProviderCellNamesTheTasksOnlyAgent(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	line2 := func(m model) string {
		_, line2 := m.renderRow(0, m.rows[0], m.totalWidth())
		// The selected row starts with the selection border.
		return strings.TrimLeft(stripANSI(line2), "│ ")
	}

	require.True(t, strings.HasPrefix(line2(m), "codex  —"), line2(m))

	// A Codex task whose only agent is Claude shows Claude.
	claudeOnly := withAgentSessions(t, m, "task-1", claudeAgent())
	require.True(t, strings.HasPrefix(line2(claudeOnly), "claude —"), line2(claudeOnly))

	// With sub-rows, each names its own provider and the task row names none.
	mixed := withAgentSessions(t, m, "task-1", codexAgent(), claudeAgent())
	require.True(t, strings.HasPrefix(line2(mixed), "—"), line2(mixed))
	require.NotContains(t, line2(mixed), "claude")
	require.NotContains(t, line2(mixed), "codex")
	view := stripANSI(mixed.View().Content)
	require.Contains(t, view, "├ codex ")
	require.Contains(t, view, "└ claude ")

	twoCodex := codexAgent()
	twoCodex.ID = "agent-codex-2"
	sameProvider := withAgentSessions(t, m, "task-1", codexAgent(), twoCodex)
	require.True(t, strings.HasPrefix(line2(sameProvider), "—"), line2(sameProvider))
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

	// On the task row, the detail panel describes the task, with the
	// providers its row lists.
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "state  ● working")
	require.Contains(t, view, "provider  claude+codex")
	require.NotContains(t, view, "pane   window")

	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)

	view = stripANSI(m.View().Content)
	require.Contains(t, view, "state  ● working in background")
	require.Contains(t, view, "       2 subagents, 1 shell")
	require.Contains(t, view, "provider  claude\n")
	require.Contains(t, view, "pane   window 0 (task) · pane 0")
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

func TestModel_AgentSubRowTruncatesLongPromptsAndKeepsItsStatusColumn(t *testing.T) {
	for name, prompt := range map[string]string{
		"ascii":              strings.Repeat("retry the flaky integration test ", 4),
		"variation selector": strings.Repeat("⚠️ fix the retry logic ✔️ ", 6),
	} {
		t.Run(name, func(t *testing.T) {
			m := agentsTaskModel(newFrontendHarness())
			long := codexAgent()
			long.LatestPrompt = prompt
			m = withAgentSessions(t, m, "task-1", claudeAgent(), long)

			// Truncated, the prompt stops a column short of the status.
			subRow := viewLineContaining(t, m, "└ codex")
			require.Regexp(t, "… +● working", subRow)
			require.Equal(
				t,
				statusColumn(t, viewLineContaining(t, m, "first task"), "◐ needs input"),
				statusColumn(t, subRow, "● working"),
			)
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

		// The header and the task rows already overflow the narrowest widths;
		// sub-rows never do.
		for _, line := range strings.Split(m.View().Content, "\n") {
			if plain := stripANSI(line); strings.Contains(plain, "├") || strings.Contains(plain, "└") {
				require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, plain)
			}
		}
		// From 44 columns a sub-row's status lines up with its task's, with
		// what room is left for the prompt.
		if width >= 44 {
			require.Equal(
				t,
				statusColumn(t, viewLineContaining(t, m, "first task"), "◐ needs input"),
				statusColumn(t, viewLineContaining(t, m, "└ codex"), "● working"),
				"width %d", width,
			)
		}
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
		view := selectBusy(width).View().Content
		panel := view[strings.Index(view, "WORKSPACE"):]
		for _, line := range strings.Split(panel, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, stripANSI(line))
		}
	}

	// At 80 columns each part of a value too wide for one line continues,
	// whole, under it.
	m := selectBusy(80)
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "state  ● working in background\n")
	require.Contains(t, view, "pane   window 3 (claude)\n")
	require.Equal(
		t,
		statusColumn(t, viewLineContaining(t, m, "state  "), "● working"),
		statusColumn(t, viewLineContaining(t, m, "2 subagents"), "2 subagents, 1 shell"),
	)
	require.Equal(
		t,
		statusColumn(t, viewLineContaining(t, m, "pane   "), "window 3 (claude)"),
		statusColumn(t, viewLineContaining(t, m, "pane 1"), "pane 1"),
	)
}

func TestModel_DetailProviderMatchesTheTaskRowsProviderCell(t *testing.T) {
	m := agentsTaskModel(newFrontendHarness())
	require.Contains(t, stripANSI(m.View().Content), "provider  codex")

	// A Codex task whose only agent is Claude says Claude in both places.
	m = withAgentSessions(t, m, "task-1", claudeAgent())
	require.True(t, strings.HasPrefix(strings.TrimLeft(viewLineContaining(t, m, "—"), "│ "), "claude "))
	require.Contains(t, stripANSI(m.View().Content), "provider  claude")
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

	require.Less(t, strings.Index(view, "├ claude"), strings.Index(view, "└ codex"))
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

	require.Contains(t, viewLineContaining(t, m, "└ codex"), "◐ working · command")
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
	require.Contains(t, viewLineContaining(t, m, "└ codex"), "first line second red line third")
	plain := stripANSI(view)
	require.Contains(t, plain, "run the green suite")
	require.Contains(t, plain, "done all passing")
}
