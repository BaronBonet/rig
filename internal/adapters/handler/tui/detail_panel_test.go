package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// detailLines returns the selected task's detail panel, one entry per line
// without styling or the panel's gutter.
func detailLines(m model) []string {
	var lines []string
	for _, line := range strings.Split(stripANSI(m.selectedTaskDetailView()), "\n") {
		lines = append(lines, strings.TrimPrefix(line, detailGutter))
	}
	return lines
}

// detailSection returns the lines of the detail panel's section under header,
// down to the blank line that ends it, or nil when the panel has no such
// section.
func detailSection(m model, header string) []string {
	lines := detailLines(m)
	start := slices.Index(lines, header)
	if start < 0 {
		return nil
	}
	section := lines[start+1:]
	if end := slices.Index(section, ""); end >= 0 {
		section = section[:end]
	}
	return section
}

// billingTaskModel is a loaded model, 100 columns wide, with the cursor on a
// Claude task that has everything the detail panel shows: one live agent with
// the model it runs on, its context and compactions, pull requests and
// unshipped work, a parent task and a child task, a prompt, activity and token
// usage. Claude's model list names its models under aliases.
func billingTaskModel(t *testing.T) model {
	t.Helper()
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "epic", RepoName: "billing", DisplayName: "Billing epic"},
		{
			ID:             "export",
			RepoName:       "billing",
			DisplayName:    "Billing export",
			Provider:       core.ProviderClaude,
			Model:          "opus",
			ReasoningLevel: "high",
			Prompt:         "Add CSV export to the billing page, behind the reports flag",
			ParentTaskID:   "epic",
		},
		{ID: "follow-up", RepoName: "billing", DisplayName: "Billing follow-up", ParentTaskID: "export"},
	}
	m := newLoadedModel(frontend)
	m.width = 100
	m.setProviderModels(core.ProviderClaude, []core.ProviderModel{
		{ID: "opus", ResolvedID: "claude-opus-5-5", DisplayName: "Opus 5.5"},
		{ID: "sonnet", ResolvedID: "claude-sonnet-5-5", DisplayName: "Sonnet 5.5"},
	}, nil)
	m = withAgentSessions(t, m, "export", claudeAgent())
	m = withTaskUsage(t, m, "export", &core.TaskUsage{
		Tokens: &core.TaskTokenUsage{
			SessionCount: 2,
			InputTokens:  980_000,
			OutputTokens: 220_000,
			TotalTokens:  1_200_000,
		},
		AgentContext:     map[string]core.ContextUsage{"agent-claude": {Tokens: 410_000, Window: 1_000_000}},
		AgentCompactions: map[string]core.Compactions{"agent-claude": {Total: 2, Auto: 2}},
		AgentModels: map[string]core.AgentModel{
			"agent-claude": {Model: "claude-opus-5-5", ReasoningLevel: "xhigh"},
		},
	})
	m = selectTask(t, m, "export")
	m.rows[m.selected].pullRequests = billingPullRequests()
	m.rows[m.selected].unshippedWork = core.UnshippedWork{Commits: 2, UncommittedChanges: true}
	m.rows[m.selected].activity = []core.TaskActivityEvent{
		{AgentSessionID: "agent-claude", Role: core.TaskActivityRoleUser, Text: "make the export include tax lines"},
		{
			AgentSessionID: "agent-claude",
			Role:           core.TaskActivityRoleAssistant,
			Text: "Added tax lines to the CSV writer and updated the tests. " +
				"Running go test ./internal/billing/... to check them.",
		},
	}
	return m
}

func TestModel_DetailPanelLaysOutEverySectionInOrder(t *testing.T) {
	m := billingTaskModel(t)

	require.Equal(t, []string{
		"Opus 5.5 · xhigh    context 41% · 410.0k of 1.0m    compacted 2× (auto)",
		"",
		"PULL REQUESTS",
		iconDone + " #120 merged  feat/billing-export",
		iconDone + " #123 merged  feat/billing-export",
		iconStatusActive + " #127 open    feat/billing-export-2",
		"+ 2 commits and uncommitted changes not in a pull request",
		"",
		"LINKED TASKS",
		"from   Billing epic",
		"spawned",
		detailValueIndent + iconStatusNone + " Billing follow-up",
		"",
		"PROMPT",
		"Add CSV export to the billing page, behind the reports flag",
		"",
		"tokens  total 1.2m · input 980.0k · output 220.0k · 2 sessions",
		"",
		"LATEST",
		"you     make the export include tax lines",
		"claude  Added tax lines to the CSV writer and updated the tests. Running go test",
		"        ./internal/billing/... to check them.",
	}, detailLines(m))

	// The panel no longer states the task's status or how long it has run.
	view := stripANSI(m.selectedTaskDetailView())
	require.NotContains(t, view, "state")
	require.NotContains(t, view, "time")

	// The provider's name is in its color.
	require.Contains(t, m.selectedTaskDetailView(), claudeStyle.Render("claude"))
}

// A task row with one agent names the model that agent runs on rather than
// the task's Launch choice, so it follows a /model switch, by the name the
// provider's model list gives it.
func TestModel_DetailPanelNamesTheModelItsAgentRuns(t *testing.T) {
	m := billingTaskModel(t)
	summaryModel := func(running map[string]core.AgentModel) string {
		m.rows[m.selected].usage.AgentModels = running
		return strings.Split(detailLines(m)[0], detailSummaryGap)[0]
	}

	require.Equal(t, "Sonnet 5.5 · medium", summaryModel(map[string]core.AgentModel{
		"agent-claude": {Model: "claude-sonnet-5-5", ReasoningLevel: "medium"},
	}))

	// Before the agent's first turn its hooks' model stands in, whose [1m]
	// suffix the list leaves out. Its reasoning level is unknown, but it runs
	// the task's Launch model, so it was launched with the task's level.
	require.Equal(t, "Opus 5.5 · high", summaryModel(map[string]core.AgentModel{
		"agent-claude": {Model: "claude-opus-5-5[1m]"},
	}))
	// On another model, the level stays unknown.
	require.Equal(t, "Sonnet 5.5", summaryModel(map[string]core.AgentModel{
		"agent-claude": {Model: "claude-sonnet-5-5"},
	}))

	// A model the list lacks reads by its ID.
	require.Equal(t, "claude-opus-6 · max", summaryModel(map[string]core.AgentModel{
		"agent-claude": {Model: "claude-opus-6", ReasoningLevel: "max"},
	}))

	// Until the agent's model is known, the task's Launch choice stands in.
	require.Equal(t, "Opus 5.5 · high", summaryModel(nil))
}

func TestModel_DetailPanelFitsNarrowTerminals(t *testing.T) {
	for _, width := range []int{40, 60, 80} {
		m := billingTaskModel(t)
		m.width = width
		for _, line := range strings.Split(m.selectedTaskDetailView(), "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width-len(detailGutter), "%d: %q", width, stripANSI(line))
		}
	}

	// At 60 columns, the tokens wrap whole fields under the first.
	m := billingTaskModel(t)
	m.width = 60
	lines := detailLines(m)
	start := slices.Index(lines, "tokens  total 1.2m · input 980.0k · output 220.0k")
	require.GreaterOrEqual(t, start, 0, "%q", lines)
	require.Equal(t, "        2 sessions", lines[start+1])
}

// A fresh task without a Launch choice has nothing for its summary line: the
// CLI's default model says nothing.
func TestModel_DetailPanelOfAFreshTaskIsItsPrompt(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{{
		ID:          "task-1",
		RepoName:    "repo",
		DisplayName: "first task",
		Provider:    core.ProviderCodex,
		Prompt:      "Add CSV export to the billing page",
	}}
	m := newLoadedModel(frontend)
	m.width = 100

	// No section shows a header without content under it.
	require.Equal(t, []string{"PROMPT", "Add CSV export to the billing page"}, detailLines(m))
}

func TestModel_DetailSummaryWrapsWholeParts(t *testing.T) {
	m := selectTask(t, contextAgentsModel(t, 96), "task-2")
	m.rows[1].task.Model = "gpt-6-sol"
	require.Equal(t, []string{"gpt-6-sol    context 41% · 106.0k of 258.4k    compacted 1× (manual)"}, detailLines(m))

	m.width = 50
	require.Equal(t, []string{"gpt-6-sol    context 41% · 106.0k of 258.4k", "compacted 1× (manual)"}, detailLines(m))

	// A part too wide for a line of its own is cut short.
	m.width = 40
	m.rows[1].task.Model = strings.Repeat("gpt-6-sol-preview-", 3)
	lines := detailLines(m)
	require.Len(t, lines, 3)
	require.Equal(t, 40-2*len(detailGutter), lipgloss.Width(lines[0]))
	require.True(t, strings.HasSuffix(lines[0], "…"), lines[0])
	require.Equal(t, []string{"context 41% · 106.0k of 258.4k", "compacted 1× (manual)"}, lines[1:])
}

func TestModel_DetailPanelDescribesTheSelectedAgent(t *testing.T) {
	m := contextAgentsModel(t, 96)
	m.rows[0].activity = []core.TaskActivityEvent{
		{AgentSessionID: "agent-claude", Role: core.TaskActivityRoleUser, Text: "make the export include tax lines"},
		{AgentSessionID: "agent-claude", Role: core.TaskActivityRoleAssistant, Text: "Added tax lines."},
		{AgentSessionID: "agent-codex", Role: core.TaskActivityRoleAssistant, Text: "Reviewed the retry change."},
	}

	// The task row's exchange mixes a Claude and a Codex agent's, so it names
	// neither provider.
	require.Equal(t, []string{
		"you        make the export include tax lines",
		"assistant  Reviewed the retry change.",
		"           Added tax lines.",
	}, detailSection(m, "LATEST"))

	// The summary leaves the task's Launch choice out, and the latest exchange
	// is the agent's own, under its provider's name rather than the task's.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	require.Equal(t, []string{
		"context 64% · 128.0k of 200.0k    window 0 (task) · pane 0",
		"",
		"LATEST",
		"you     make the export include tax lines",
		"claude  Added tax lines.",
	}, detailLines(m))

	// Without a prompt in its activity, the agent's latest prompt stands in.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-codex", m.selectedAgent)
	require.Equal(t, []string{
		"context 9% · 24.5k of 258.4k    compacted 2×    window 2 (review) · pane 1",
		"",
		"LATEST",
		"you    review the retry change",
		"codex  Reviewed the retry change.",
	}, detailLines(m))
}

func TestModel_DetailPanelKeepsTheCauseOfAFailedCreation(t *testing.T) {
	m := billingTaskModel(t)
	task := m.rows[m.selected].task
	task.CreationStatus = core.TaskCreationStatusFailed
	task.CreationError = "setup script \"/repo/.rig/setup.sh\" failed: exit status 1\nlast output:\n" +
		"npm ERR! missing script: build"

	lines := detailLines(m)
	require.True(t, strings.HasPrefix(lines[0], "Failed while "), lines[0])
	require.Equal(
		t,
		`setup script "/repo/.rig/setup.sh" failed: exit status 1 last output: npm ERR! missing script:`,
		lines[1],
	)
	require.Equal(t, "build", lines[2])
}

func TestWithoutDanglingEnd_DropsBlankLinesAndAnEmptySectionHeader(t *testing.T) {
	summary, header := detailGutter+"Opus 5.5", detailGutter+"LATEST"
	require.Equal(t, []string{"─", summary}, withoutDanglingEnd([]string{"─", summary, "", header}))
	require.Equal(t, []string{"─", summary}, withoutDanglingEnd([]string{"─", summary, ""}))
	tokens := detailGutter + "tokens  total 1.2m"
	require.Equal(t, []string{"─", summary, "", tokens}, withoutDanglingEnd([]string{"─", summary, "", tokens, ""}))
	require.Equal(
		t,
		[]string{"─", summary, "", header, detailGutter + "you  hi"},
		withoutDanglingEnd([]string{"─", summary, "", header, detailGutter + "you  hi"}),
	)
}

func TestModel_DetailPanelColorsWhatItNames(t *testing.T) {
	view := billingTaskModel(t).selectedTaskDetailView()

	require.Contains(t, view, claudeStyle.Render("Opus 5.5"))
	require.Contains(t, view, detailHeaderStyle.Render("PULL REQUESTS"))
	require.Contains(t, view, userLabelStyle.Render("you"))
	require.Contains(t, view, primaryStyle.Bold(true).Render("1.2m"))
}

func TestContextStyle_WarnsAsTheWindowFills(t *testing.T) {
	// The styles render apart, so comparing their output tells them apart.
	require.NotEqual(t, primaryStyle.Render("x"), warningStyle.Render("x"))
	for _, tc := range []struct {
		context *core.ContextUsage
		want    lipgloss.Style
	}{
		{nil, primaryStyle},
		{&core.ContextUsage{Tokens: 500}, primaryStyle},
		{&core.ContextUsage{Tokens: 69, Window: 100}, primaryStyle},
		{&core.ContextUsage{Tokens: 70, Window: 100}, warningStyle},
		{&core.ContextUsage{Tokens: 89, Window: 100}, warningStyle},
		{&core.ContextUsage{Tokens: 90, Window: 100}, errorStyle},
	} {
		require.Equal(t, tc.want.Render("x"), contextStyle(tc.context).Render("x"), "%+v", tc.context)
	}
}

func TestListedModelID_MatchesAcrossAliasesAndSuffixes(t *testing.T) {
	models := []core.ProviderModel{
		{ID: "opus[1m]", ResolvedID: "claude-opus-5-5[1m]"},
		{ID: "gpt-6-sol"},
	}
	require.Equal(t, "opus[1m]", listedModelID(models, "claude-opus-5-5"))
	require.Equal(t, "opus[1m]", listedModelID(models, "Claude-Opus-5-5[1m]"))
	require.Equal(t, "gpt-6-sol", listedModelID(models, "gpt-6-sol"))
	require.Equal(t, "claude-haiku-5-5", listedModelID(models, "claude-haiku-5-5"))
}
