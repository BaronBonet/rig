package tui

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func pullRequest(number int, state core.PRState, headBranch string) core.PullRequest {
	return core.PullRequest{Number: number, State: state, HeadBranch: headBranch}
}

// billingPullRequests are a task's two merged pull requests and the open one
// it has made since, in a new branch in the same worktree.
func billingPullRequests() []core.PullRequest {
	return []core.PullRequest{
		pullRequest(120, core.PRStateMerged, "feat/billing-export"),
		pullRequest(123, core.PRStateMerged, "feat/billing-export"),
		pullRequest(127, core.PRStateOpen, "feat/billing-export-2"),
	}
}

func TestCurrentPullRequest_IsTheNewestOpenOrDraftElseTheNewest(t *testing.T) {
	cases := map[string]struct {
		pullRequests []core.PullRequest
		current      int
	}{
		"none": {nil, -1},
		"newest open": {[]core.PullRequest{
			pullRequest(120, core.PRStateOpen, ""),
			pullRequest(123, core.PRStateMerged, ""),
			pullRequest(127, core.PRStateOpen, ""),
		}, 2},
		"open before a newer merged one": {[]core.PullRequest{
			pullRequest(127, core.PRStateOpen, ""),
			pullRequest(130, core.PRStateMerged, ""),
		}, 0},
		"newest draft": {[]core.PullRequest{
			pullRequest(127, core.PRStateOpen, ""),
			pullRequest(129, core.PRStateDraft, ""),
			pullRequest(130, core.PRStateClosed, ""),
		}, 1},
		"none open, the newest": {[]core.PullRequest{
			pullRequest(120, core.PRStateMerged, ""),
			pullRequest(121, core.PRStateClosed, ""),
		}, 1},
	}
	for name, tc := range cases {
		require.Equal(t, tc.current, currentPullRequest(tc.pullRequests), name)
	}
}

func TestPullRequestsRowText_LeadsWithTheOtherPullRequestsAndEndsWithTheCurrentOne(t *testing.T) {
	cases := map[string]struct {
		pullRequests []core.PullRequest
		marker       string
	}{
		"merged twice, then open": {billingPullRequests(), iconDone + iconDone + iconStatusActive + "#127"},
		"merged only": {
			[]core.PullRequest{pullRequest(119, core.PRStateMerged, "")},
			iconDone + "#119",
		},
		"the current one goes last": {
			[]core.PullRequest{pullRequest(127, core.PRStateOpen, ""), pullRequest(130, core.PRStateMerged, "")},
			iconDone + iconStatusActive + "#127",
		},
		"a closed one shows as the current one": {
			[]core.PullRequest{pullRequest(120, core.PRStateMerged, ""), pullRequest(121, core.PRStateClosed, "")},
			iconDone + iconFailed + "#121",
		},
		"other closed ones are left out": {
			[]core.PullRequest{
				pullRequest(118, core.PRStateClosed, ""),
				pullRequest(120, core.PRStateMerged, ""),
				pullRequest(125, core.PRStateDraft, ""),
			},
			iconDone + iconStatusProgress + "#125",
		},
		"none": {nil, ""},
	}
	for name, tc := range cases {
		require.Equal(
			t,
			tc.marker,
			stripANSI(pullRequestsRowText(pullRequestsMarker{pullRequests: tc.pullRequests}, 100)),
			name,
		)
	}
}

func TestPullRequestsRowText_ColorsEachSymbolByItsState(t *testing.T) {
	pullRequests := []core.PullRequest{
		pullRequest(120, core.PRStateMerged, ""),
		pullRequest(125, core.PRStateDraft, ""),
		pullRequest(127, core.PRStateOpen, ""),
	}

	require.Equal(
		t,
		prMergedStyle.Render(
			iconDone,
		)+warningStyle.Render(
			iconStatusProgress,
		)+healthyStyle.Render(
			iconStatusActive+"#127",
		),
		pullRequestsRowText(pullRequestsMarker{pullRequests: pullRequests}, 100),
	)
}

func TestRenderRow_ShowsEveryPullRequestAfterTheName(t *testing.T) {
	row := taskRow{task: rowTask(), pullRequests: billingPullRequests()}

	line := rowContent(model{}.renderRow(0, row, 100))

	require.Contains(t, line, "first task  "+iconDone+iconDone+iconStatusActive+"#127  ")
	require.NotContains(t, line, "#120")
	require.NotContains(t, line, "#123")
}

func TestRenderRow_LongNameKeepsAllItsPullRequests(t *testing.T) {
	row := taskRow{task: rowTask(), pullRequests: billingPullRequests()}
	row.task.DisplayName = strings.Repeat("very long task name ", 10)

	for _, width := range []int{40, 54, 62, 100, 140} {
		line := model{}.renderRow(0, row, width)

		require.Less(t, lipgloss.Width(line), width, "width %d", width)
		require.Contains(t, rowContent(line), "…  "+iconDone+iconDone+iconStatusActive+"#127", "width %d", width)
	}
}

// manyPullRequests are count merged pull requests from #1, then open #1234.
func manyPullRequests(count int) []core.PullRequest {
	pullRequests := make([]core.PullRequest, 0, count+1)
	for number := 1; number <= count; number++ {
		pullRequests = append(pullRequests, pullRequest(number, core.PRStateMerged, "feat/part-"+strconv.Itoa(number)))
	}
	return append(pullRequests, pullRequest(1234, core.PRStateOpen, "feat/now"))
}

func TestPullRequestsRowText_CollapsesTheOldestEarlierOnesIntoAnEllipsis(t *testing.T) {
	cases := map[string]struct {
		pullRequests []core.PullRequest
		marker       string
	}{
		"seven earlier": {manyPullRequests(7), "…" + strings.Repeat(iconDone, 4) + iconStatusActive + "#1234"},
		"four earlier":  {manyPullRequests(4), strings.Repeat(iconDone, 4) + iconStatusActive + "#1234"},
		"five earlier":  {manyPullRequests(5), "…" + strings.Repeat(iconDone, 4) + iconStatusActive + "#1234"},
		"closed ones do not count": {
			[]core.PullRequest{
				pullRequest(1, core.PRStateClosed, ""),
				pullRequest(2, core.PRStateMerged, ""),
				pullRequest(3, core.PRStateClosed, ""),
				pullRequest(4, core.PRStateMerged, ""),
				pullRequest(5, core.PRStateMerged, ""),
				pullRequest(6, core.PRStateMerged, ""),
				pullRequest(1234, core.PRStateOpen, ""),
			},
			strings.Repeat(iconDone, 4) + iconStatusActive + "#1234",
		},
	}
	for name, tc := range cases {
		require.Equal(
			t,
			tc.marker,
			stripANSI(pullRequestsRowText(pullRequestsMarker{pullRequests: tc.pullRequests}, 100)),
			name,
		)
	}
	// The ellipsis is muted, as it stands for pull requests of any state.
	require.True(
		t,
		strings.HasPrefix(
			pullRequestsRowText(pullRequestsMarker{pullRequests: manyPullRequests(7)}, 100),
			mutedStyle.Render("…"),
		),
	)
}

// A marker too wide for its room leaves out more earlier pull requests, but
// always shows the current one.
func TestPullRequestsRowText_LeavesOutMoreEarlierOnesToFitItsRoom(t *testing.T) {
	cases := []struct {
		pullRequests []core.PullRequest
		room         int
		marker       string
	}{
		{manyPullRequests(7), 11, "…" + strings.Repeat(iconDone, 4) + iconStatusActive + "#1234"},
		{manyPullRequests(7), 9, "…" + strings.Repeat(iconDone, 2) + iconStatusActive + "#1234"},
		{manyPullRequests(7), 7, "…" + iconStatusActive + "#1234"},
		{manyPullRequests(7), 2, "…" + iconStatusActive + "#1234"},
		{manyPullRequests(3), 9, strings.Repeat(iconDone, 3) + iconStatusActive + "#1234"},
		{manyPullRequests(3), 8, "…" + iconDone + iconStatusActive + "#1234"},
		{manyPullRequests(0), 2, iconStatusActive + "#1234"},
	}
	for _, tc := range cases {
		marker := pullRequestsRowText(pullRequestsMarker{pullRequests: tc.pullRequests}, tc.room)
		require.Equal(t, tc.marker, stripANSI(marker), "room %d", tc.room)
	}
}

// However many pull requests a task has, its row stays within the terminal.
func TestRenderRow_ManyPullRequestsNeverWidenTheRow(t *testing.T) {
	row := taskRow{task: rowTask(), pullRequests: manyPullRequests(30)}

	for width := 30; width <= 140; width++ {
		line := model{}.renderRow(0, row, width)

		require.Less(t, lipgloss.Width(line), width, "width %d: %q", width, stripANSI(line))
		view := rowContent(line)
		require.Contains(t, view, "…", "width %d", width)
		require.Contains(t, view, iconStatusActive+"#1234", "width %d", width)
		require.LessOrEqual(t, strings.Count(view, iconDone), maxEarlierPullRequestSymbols, "width %d", width)
	}

	// Where the whole marker fits beside the name, it shows whole; narrower,
	// earlier pull requests give way before the name shrinks below eight
	// cells.
	allFour := "…" + strings.Repeat(iconDone, 4) + iconStatusActive + "#1234"
	require.Contains(t, rowContent(model{}.renderRow(0, row, 40)), "first task  "+allFour)
	require.Contains(t, rowContent(model{}.renderRow(0, row, 34)), "first t…  …"+iconDone+iconStatusActive+"#1234")
}

func TestPullRequestDetailLines_ListsEveryPullRequest(t *testing.T) {
	lines := pullRequestDetailLines(billingPullRequests(), 60)

	require.Equal(t, []string{
		iconDone + " #120 merged  feat/billing-export",
		iconDone + " #123 merged  feat/billing-export",
		iconStatusActive + " #127 open    feat/billing-export-2",
	}, stripLines(lines))
	require.Empty(t, pullRequestDetailLines(nil, 60))
}

func TestPullRequestDetailLines_LinesUpNumbersOfEveryWidth(t *testing.T) {
	lines := pullRequestDetailLines([]core.PullRequest{
		pullRequest(99, core.PRStateClosed, "feat/a"),
		pullRequest(100, core.PRStateDraft, "feat/b"),
	}, 60)

	require.Equal(t, []string{
		iconFailed + " #99  closed  feat/a",
		iconStatusProgress + " #100 draft   feat/b",
	}, stripLines(lines))
}

func TestPullRequestDetailLines_ShowFiveAtMost(t *testing.T) {
	five := pullRequestDetailLines(manyPullRequests(4), 60)
	require.Equal(t, []string{
		iconDone + " #1    merged  feat/part-1",
		iconDone + " #2    merged  feat/part-2",
		iconDone + " #3    merged  feat/part-3",
		iconDone + " #4    merged  feat/part-4",
		iconStatusActive + " #1234 open    feat/now",
	}, stripLines(five))

	// Past five, the earlier ones are counted and the newest four listed.
	lines := pullRequestDetailLines(manyPullRequests(6), 60)
	require.Equal(t, []string{
		"3 earlier",
		iconDone + " #4    merged  feat/part-4",
		iconDone + " #5    merged  feat/part-5",
		iconDone + " #6    merged  feat/part-6",
		iconStatusActive + " #1234 open    feat/now",
	}, stripLines(lines))
	require.Equal(t, mutedStyle.Render("3 earlier"), lines[0])
}

func TestPullRequestDetailLines_FitTheColumn(t *testing.T) {
	lines := pullRequestDetailLines([]core.PullRequest{
		pullRequest(127, core.PRStateOpen, "feat/"+strings.Repeat("very-long-branch-", 5)),
	}, 42)

	require.Equal(t, 42, lipgloss.Width(lines[0]))
	require.True(t, strings.HasSuffix(stripANSI(lines[0]), "…"), stripANSI(lines[0]))
}

func TestModel_DetailPanelListsThePullRequestsInTheirSection(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m.rows[0].task.BranchName = "feat/billing-export-2"
	m.rows[0].pullRequests = billingPullRequests()

	require.Equal(t, []string{
		iconDone + " #120 merged  feat/billing-export",
		iconDone + " #123 merged  feat/billing-export",
		iconStatusActive + " #127 open    feat/billing-export-2",
	}, detailSection(m, "PULL REQUESTS"))

	// A task without pull requests has no such section.
	m.rows[0].pullRequests = nil
	require.Nil(t, detailSection(m, "PULL REQUESTS"))
}

func TestModel_PullRequestsRefreshOnATimer(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	frontend.taskPullRequests = map[string]core.TaskPullRequests{"task-1": {PullRequests: billingPullRequests()}}

	next, cmd := m.Update(pullRequestRefreshTickMsg{})
	m = asModel(t, next)
	msgs := runBatchCmd(t, cmd)

	// The tick refreshes every task's pull requests and comes round again.
	require.Equal(t, []string{"task-1", "task-2"}, frontend.taskPullRequestsCalls)
	requireMsgType[pullRequestRefreshTickMsg](t, msgs)
	for _, msg := range msgsOfType[taskPullRequestsLoadedMsg](msgs) {
		next, _ = m.Update(msg)
		m = asModel(t, next)
	}
	require.Equal(t, billingPullRequests(), m.rows[0].pullRequests)
	require.Empty(t, m.rows[1].pullRequests)
	require.Contains(t, stripANSI(m.View().Content), iconDone+iconDone+iconStatusActive+"#127")

	// A pull request merged on GitHub shows at the next tick.
	merged := billingPullRequests()
	merged[2].State = core.PRStateMerged
	frontend.taskPullRequests["task-1"] = core.TaskPullRequests{PullRequests: merged}
	m = applyPullRequestRefresh(t, m)
	require.Equal(t, merged, m.rows[0].pullRequests)
	require.Contains(t, stripANSI(m.View().Content), iconDone+iconDone+iconDone+"#127")
}

func TestModel_FailedPullRequestRefreshKeepsTheLastLoadedOnes(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	frontend.taskPullRequests = map[string]core.TaskPullRequests{"task-1": {PullRequests: billingPullRequests()}}
	m = applyPullRequestRefresh(t, m)

	frontend.taskPullRequestsErr = map[string]error{"task-1": errors.New("gh: not logged in")}
	m = applyPullRequestRefresh(t, m)

	require.Equal(t, billingPullRequests(), m.rows[0].pullRequests)
	require.NoError(t, m.err)
}

func TestModel_ReloadKeepsThePullRequestsUntilTheyRefresh(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m.rows[0].pullRequests = billingPullRequests()

	next, cmd := m.Update(tea.KeyPressMsg{Text: "r"})
	m = asModel(t, next)
	next, _ = m.Update(runCmd(t, cmd))
	m = asModel(t, next)

	require.Equal(t, "task-1", m.rows[0].task.ID)
	require.Equal(t, billingPullRequests(), m.rows[0].pullRequests)
}

// The pull requests marker sits on the task row of a child task with agent
// sub-rows, which keep nesting under the task.
func TestModel_PullRequestsFitAChildTaskWithAgentSubRows(t *testing.T) {
	type pullRequestsCase struct {
		pullRequests core.TaskPullRequests
		marker       string
		current      string
	}
	pullRequestsCases := []pullRequestsCase{
		{
			core.TaskPullRequests{PullRequests: billingPullRequests()},
			iconDone + iconDone + iconStatusActive + "#127",
			"#127",
		},
		{
			core.TaskPullRequests{PullRequests: manyPullRequests(30)},
			"…" + strings.Repeat(iconDone, 4) + iconStatusActive + "#1234",
			"#1234",
		},
		{
			core.TaskPullRequests{
				PullRequests:  manyPullRequests(30),
				UnshippedWork: core.UnshippedWork{Commits: 2, UncommittedChanges: true},
			},
			"…" + strings.Repeat(iconDone, 4) + iconStatusActive + "#1234+",
			"#1234",
		},
	}
	for _, width := range []int{40, 48, 120} {
		for _, tc := range pullRequestsCases {
			assertChildTaskWithAgentSubRowsFits(t, width, tc.pullRequests, tc.marker, tc.current)
		}
	}
}

// assertChildTaskWithAgentSubRowsFits renders, width columns wide, a child
// task with two agents and pullRequests, whose row's pull requests marker is
// marker, holding the current pull request's number, current.
func assertChildTaskWithAgentSubRowsFits(
	t *testing.T,
	width int,
	pullRequests core.TaskPullRequests,
	marker string,
	current string,
) {
	t.Helper()
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m.width = width
	m.detailsHidden = true
	m.rows[0].task.ParentTaskID = "task-2"
	m.rows[0].pullRequests = pullRequests.PullRequests
	m.rows[0].unshippedWork = pullRequests.UnshippedWork
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())

	// The name may be truncated to fit the marker.
	taskLine := viewLineContaining(t, m, iconChildTask)
	require.True(
		t,
		strings.HasPrefix(rowContent(taskLine), iconStatusNeedsInput+" "+iconChildTask+" first t"),
		"width %d: %q", width, taskLine,
	)
	require.Contains(t, taskLine, "  "+marker+"  ", "width %d", width)
	// The sub-rows start where the task's name area does, at its link marker.
	claudeLine := viewLineContaining(t, m, "├")
	codexLine := viewLineContaining(t, m, "└")
	nameArea := statusColumn(t, taskLine, iconChildTask)
	require.Equal(t, nameArea, statusColumn(t, claudeLine, "├"), "width %d", width)
	require.Equal(t, nameArea, statusColumn(t, codexLine, "└"), "width %d", width)
	for _, subRow := range []string{claudeLine, codexLine} {
		require.NotContains(t, subRow, current, "width %d", width)
	}

	// The keybind header already overflows the narrowest widths; task rows
	// and sub-rows never do.
	for _, line := range strings.Split(m.View().Content, "\n")[1:] {
		require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, stripANSI(line))
	}
	// The marker leaves the columns on the right in line with the next
	// task's.
	require.Equal(
		t,
		lipgloss.Width(taskLine),
		lipgloss.Width(viewLineContaining(t, m, "second task")),
		"width %d", width,
	)
}

// applyPullRequestRefresh runs one pull request refresh tick and applies the
// pull requests it loads.
func applyPullRequestRefresh(t *testing.T, m model) model {
	t.Helper()
	next, cmd := m.Update(pullRequestRefreshTickMsg{})
	m = asModel(t, next)
	for _, msg := range msgsOfType[taskPullRequestsLoadedMsg](runBatchCmd(t, cmd)) {
		next, _ = m.Update(msg)
		m = asModel(t, next)
	}
	return m
}

func stripLines(lines []string) []string {
	stripped := make([]string, 0, len(lines))
	for _, line := range lines {
		stripped = append(stripped, stripANSI(line))
	}
	return stripped
}
