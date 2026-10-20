package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

const (
	backendTaskName  = "Backend: add orders endpoint"
	frontendTaskName = "Frontend: call orders endpoint"
	docsTaskName     = "Docs: document orders endpoint"
)

// linkedTasksModel is a loaded model, 100 columns wide, in which backend-api
// spawned frontend-ui in another repo and backend-docs, whose creation failed,
// in its own. unlinked has no parent, and orphan's parent is no longer in the
// list. The list shows the backend repo first: backend-api, backend-docs,
// unlinked, then frontend-ui and orphan.
func linkedTasksModel() model {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "backend-api", RepoRoot: "/code/backend", RepoName: "backend", DisplayName: backendTaskName},
		{
			ID:           "frontend-ui",
			RepoRoot:     "/code/frontend",
			RepoName:     "frontend",
			DisplayName:  frontendTaskName,
			Provider:     core.ProviderCodex,
			ParentTaskID: "backend-api",
		},
		{
			ID:             "backend-docs",
			RepoRoot:       "/code/backend",
			RepoName:       "backend",
			DisplayName:    docsTaskName,
			CreationStatus: core.TaskCreationStatusFailed,
			ParentTaskID:   "backend-api",
		},
		{ID: "unlinked", RepoRoot: "/code/backend", RepoName: "backend", DisplayName: "Unrelated cleanup"},
		{
			ID:           "orphan",
			RepoRoot:     "/code/frontend",
			RepoName:     "frontend",
			DisplayName:  "Orphaned follow-up",
			ParentTaskID: "deleted-task",
		},
	}
	m := newLoadedModel(frontend)
	m.width = 100
	return m
}

// selectTask moves the cursor to the row of the task with id.
func selectTask(t *testing.T, m model, id string) model {
	t.Helper()
	for index, row := range m.rows {
		if row.task.ID == id {
			m.selected = index
			return m
		}
	}
	t.Fatalf("no row for task %q", id)
	return m
}

func TestModel_ChildTaskRowCarriesTheLinkMarker(t *testing.T) {
	m := linkedTasksModel()
	m.detailsHidden = true

	// A child task's link marker sits between its status symbol and its name.
	// A child in another repo than its parent's, which the list cannot nest
	// under it, carries a marker of its own.
	docsRow := rowContent(viewLineContaining(t, m, docsTaskName))
	require.True(t, strings.HasPrefix(docsRow, iconFailed+" "+iconChildTask+" "+docsTaskName), docsRow)
	frontendRow := rowContent(viewLineContaining(t, m, frontendTaskName))
	require.True(
		t,
		strings.HasPrefix(frontendRow, iconStatusNone+" "+iconCrossRepoChildTask+" "+frontendTaskName),
		frontendRow,
	)
	require.NotContains(t, frontendRow, iconChildTask)

	// The parent, an unlinked task and a task whose parent is gone carry none.
	for _, name := range []string{backendTaskName, "Unrelated cleanup", "Orphaned follow-up"} {
		row := rowContent(viewLineContaining(t, m, name))
		require.True(t, strings.HasPrefix(row, iconStatusNone+" "+name), row)
		require.NotContains(t, row, iconChildTask)
		require.NotContains(t, row, iconCrossRepoChildTask)
	}

	// The marker is muted rather than the color of the child's status. The
	// cursor is on backend-api, the first row, not on backend-docs, the second.
	require.Equal(t, 0, m.selected)
	require.Equal(t, "backend-docs", m.rows[1].task.ID)
	require.Contains(t, m.renderRow(1, m.rows[1], taskRowLayoutFor(100)), " "+mutedStyle.Render(iconChildTask)+" ")
}

func TestModel_SelectedChildTaskBrightensItsLinkMarker(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	frontend.listTasks[0].ParentTaskID = "task-2"
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	selectedMarker := " " + primaryStyle.Render(iconChildTask) + " "

	// Like the rest of the selected row, its marker brightens.
	require.Equal(t, 0, m.selected)
	require.Contains(t, m.View().Content, selectedMarker)

	// So it stays while the cursor is on one of the child's agent sub-rows.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	require.Contains(t, m.View().Content, selectedMarker)

	// On the next task, it is muted again.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, 1, m.selected)
	require.NotContains(t, m.View().Content, selectedMarker)
	require.Contains(t, m.View().Content, " "+mutedStyle.Render(iconChildTask)+" ")
}

func TestRenderRow_LinkMarkerKeepsTheRightColumnsAligned(t *testing.T) {
	parent := rowTask()
	parent.ID = "parent"
	linkedRow := func(parentID string) taskRow {
		row := compactedRow()
		row.task.DisplayName = strings.Repeat("very long task name ", 10)
		row.task.ParentTaskID = parentID
		row.pullRequests = []core.PullRequest{{State: core.PRStateOpen, Number: 42}}
		return row
	}
	child, unlinked := linkedRow("parent"), linkedRow("")
	m := model{rows: []taskRow{{task: parent}, child, unlinked}}

	// From wide to narrow enough to drop both the compactions and provider
	// columns, the child's name gives up the marker's width, so its columns on
	// the right start where the unlinked row's do.
	for _, width := range []int{120, 62, 54, 40} {
		childLine := stripANSI(m.renderRow(1, child, taskRowLayoutFor(width)))
		unlinkedLine := stripANSI(m.renderRow(2, unlinked, taskRowLayoutFor(width)))

		require.Contains(t, childLine, iconChildTask, "width %d", width)
		require.Less(t, lipgloss.Width(childLine), width, "width %d: %q", width, childLine)
		require.Equal(t, lipgloss.Width(unlinkedLine), lipgloss.Width(childLine), "width %d", width)
		for _, column := range []string{"…", "#42", "codex", iconCompacted + "2", "15m"} {
			require.Equal(t, strings.Contains(unlinkedLine, column), strings.Contains(childLine, column), column)
			if strings.Contains(unlinkedLine, column) {
				require.Equal(
					t,
					statusColumn(t, unlinkedLine, column),
					statusColumn(t, childLine, column),
					"width %d: %q", width, column,
				)
			}
		}
	}
}

func TestRenderRow_ChildRowIsNeverWiderThanOtherRows(t *testing.T) {
	parent := rowTask()
	parent.ID = "parent"
	linkedRow := func(parentID string) taskRow {
		row := taskRow{task: rowTask(), pullRequests: []core.PullRequest{{State: core.PRStateMerged, Number: 12345}}}
		row.task.ParentTaskID = parentID
		return row
	}
	child, unlinked := linkedRow("parent"), linkedRow("")
	m := model{rows: []taskRow{{task: parent}, child, unlinked}}

	// However narrow the terminal, the marker never widens a child's row past
	// the others.
	for width := 1; width <= 140; width++ {
		require.Equal(
			t,
			lipgloss.Width(m.renderRow(2, unlinked, taskRowLayoutFor(width))),
			lipgloss.Width(m.renderRow(1, child, taskRowLayoutFor(width))),
			"width %d: %q", width, stripANSI(m.renderRow(1, child, taskRowLayoutFor(width))),
		)
	}

	// A name area too narrow to keep a cell of the name beside the marker
	// leaves the marker out.
	narrow := rowContent(m.renderRow(1, child, taskRowLayoutFor(22)))
	require.NotContains(t, narrow, iconChildTask)
	require.Contains(t, narrow, "#12345")
}

func TestModel_AgentSubRowsRenderUnchangedUnderAChildTask(t *testing.T) {
	unlinked := withAgentSessions(t, agentsTaskModel(newFrontendHarness()), "task-1", claudeAgent(), codexAgent())
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	frontend.listTasks[0].ParentTaskID = "task-2"
	child := withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())

	childRow := viewLineContaining(t, child, "first task")
	require.Contains(t, childRow, iconStatusNeedsInput+" "+iconChildTask+" first task")
	// The child's status symbol keeps its column.
	require.Equal(
		t,
		statusColumn(t, viewLineContaining(t, unlinked, "first task"), iconStatusNeedsInput),
		statusColumn(t, childRow, iconStatusNeedsInput),
	)

	// Its sub-rows render as under any task, their branches under its link
	// marker, where an unlinked task's name starts.
	for _, prompt := range []string{"fix the flaky retry test", "review the retry change"} {
		require.Equal(t, viewLineContaining(t, unlinked, prompt), viewLineContaining(t, child, prompt))
	}
	require.Equal(
		t,
		statusColumn(t, childRow, iconChildTask),
		statusColumn(t, viewLineContaining(t, child, "fix the flaky retry test"), "├"),
	)
	require.Equal(
		t,
		statusColumn(t, childRow, iconChildTask),
		statusColumn(t, viewLineContaining(t, child, "review the retry change"), "└"),
	)
}

func TestModel_DetailViewNamesAChildsParentTask(t *testing.T) {
	m := linkedTasksModel()

	require.Equal(
		t,
		[]string{"from   " + backendTaskName},
		detailSection(selectTask(t, m, "backend-docs"), "LINKED TASKS"),
	)

	// A child from another repo also names its parent's repo.
	require.Equal(t, []string{
		"from   " + backendTaskName,
		detailValueIndent + "in backend",
	}, detailSection(selectTask(t, m, "frontend-ui"), "LINKED TASKS"))

	// A task whose parent is gone, or that has none, has no linked tasks.
	for _, id := range []string{"orphan", "unlinked"} {
		require.Nil(t, detailSection(selectTask(t, m, id), "LINKED TASKS"), id)
	}
}

func TestModel_DetailViewTruncatesLongLinkedNamesToThePanel(t *testing.T) {
	m := linkedTasksModel()
	// backend-api and frontend-ui, the first and fourth rows, get long names.
	m.rows[0].task.DisplayName = strings.Repeat("Backend: add the orders endpoint ", 5)
	m.rows[3].task.DisplayName = strings.Repeat("Frontend: call the orders endpoint ", 5)

	// A long parent name ends at the panel's right margin.
	child := selectTask(t, m, "frontend-ui")
	fromLine := viewLineContaining(t, child, "from   ")
	require.True(t, strings.HasSuffix(fromLine, "…"), fromLine)
	require.Equal(t, child.totalWidth()-len(detailGutter), lipgloss.Width(fromLine))

	// So does a long child name in the spawned list.
	childLine := strings.TrimRight(
		viewLineContaining(t, selectTask(t, m, "backend-api"), detailValueIndent+iconStatusNone+" Frontend"),
		" ",
	)
	require.True(t, strings.HasSuffix(childLine, "…"), childLine)
	require.Equal(t, child.totalWidth()-len(detailGutter), lipgloss.Width(childLine))
}

func TestModel_DetailViewShowsBothLinksOfATaskInAChain(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-a", RepoName: "repo", DisplayName: "task a"},
		{ID: "task-b", RepoName: "repo", DisplayName: "task b", ParentTaskID: "task-a"},
		{ID: "task-c", RepoName: "repo", DisplayName: "task c", ParentTaskID: "task-b"},
	}
	m := newLoadedModel(frontend)
	m.width = 100

	// The middle task came from one task and spawned another.
	require.Equal(t, []string{
		"from   task a",
		"spawned",
		detailValueIndent + iconStatusNone + " task c",
	}, detailSection(selectTask(t, m, "task-b"), "LINKED TASKS"))
	require.Contains(t, rowContent(viewLineContaining(t, m, "task b")), iconChildTask+" task b")
}

func TestModel_DetailViewListsTheTasksAParentSpawned(t *testing.T) {
	m := linkedTasksModel()
	m = withAgentSessions(t, m, "frontend-ui", claudeAgent())
	m = selectTask(t, m, "backend-api")

	// Each child shows the symbol its row leads with, in list order.
	require.Equal(t, []string{
		"spawned",
		detailValueIndent + iconFailed + " " + docsTaskName,
		detailValueIndent + iconStatusNeedsInput + " " + frontendTaskName,
	}, detailSection(m, "LINKED TASKS"))

	view := m.View().Content
	require.Contains(t, view, detailValueIndent+errorStyle.Render(iconFailed)+" ")
	require.Contains(t, view, detailValueIndent+warningStyle.Render(iconStatusNeedsInput)+" ")
}

// spawnedListOf returns the lines the detail panel lists under spawned for a
// parent task, the first row, with count child tasks.
func spawnedListOf(t *testing.T, count int) []string {
	t.Helper()
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{{ID: "parent", RepoName: "repo", DisplayName: "parent task"}}
	for i := 1; i <= count; i++ {
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID:           "child-" + strconv.Itoa(i),
			RepoName:     "repo",
			DisplayName:  "child task " + strconv.Itoa(i),
			ParentTaskID: "parent",
		})
	}
	m := newLoadedModel(frontend)
	m.width = 100

	section := detailSection(m, "LINKED TASKS")
	return section[slices.Index(section, "spawned")+1:]
}

func childTaskLines(count int) []string {
	lines := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		lines = append(lines, detailValueIndent+iconStatusNone+" child task "+strconv.Itoa(i))
	}
	return lines
}

func TestModel_DetailViewCapsTheSpawnedList(t *testing.T) {
	// One child past the cap takes the line a "+1 more" would, so it is
	// listed.
	require.Equal(t, childTaskLines(maxSpawnedTasks+1), spawnedListOf(t, maxSpawnedTasks+1))

	// Past that, the list stops at the cap and counts the rest.
	require.Equal(
		t,
		append(childTaskLines(maxSpawnedTasks), detailValueIndent+"+2 more"),
		spawnedListOf(t, maxSpawnedTasks+2),
	)
}
