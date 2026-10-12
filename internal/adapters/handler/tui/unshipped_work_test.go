package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

// billingMarker is the row marker of billingPullRequests.
const billingMarker = iconDone + iconDone + iconStatusActive + "#127"

func TestRenderRow_MarksUnshippedWorkAfterTheCurrentPullRequest(t *testing.T) {
	cases := map[string]core.UnshippedWork{
		"commits":             {Commits: 2},
		"uncommitted changes": {UncommittedChanges: true},
		"both":                {Commits: 1, UncommittedChanges: true},
	}
	for name, work := range cases {
		row := taskRow{task: rowTask(), pullRequests: billingPullRequests(), unshippedWork: work}

		line := rowContent(model{}.renderRow(0, row, taskRowLayoutFor(100)))

		require.Contains(t, line, "first task  "+billingMarker+"+  ", name)
	}

	none := taskRow{task: rowTask(), pullRequests: billingPullRequests()}
	require.NotContains(t, rowContent(model{}.renderRow(0, none, taskRowLayoutFor(100))), "+")
	// Unshipped work is only found for a task with pull requests, and a row
	// marks it only with them.
	withoutPullRequests := taskRow{task: rowTask(), unshippedWork: core.UnshippedWork{Commits: 3}}
	require.NotContains(t, rowContent(model{}.renderRow(0, withoutPullRequests, taskRowLayoutFor(100))), "+")
}

// The + is in plain text color: Rig's colors mean the task needs the user, or
// is busy, and unshipped work is neither.
func TestPullRequestsRowText_MarksUnshippedWorkInPlainTextColor(t *testing.T) {
	marker := pullRequestsRowText(pullRequestsMarker{pullRequests: billingPullRequests(), unshipped: true}, 100)

	merged := cursorLineStyle(prMergedStyle, false).Render(iconDone)
	require.Equal(
		t,
		merged+merged+cursorLineStyle(healthyStyle, false).Render(iconStatusActive+"#127")+
			cursorLineStyle(primaryStyle, false).Render("+"),
		marker,
	)
}

// Like the current pull request, the + always shows: earlier pull requests
// give way to it.
func TestPullRequestsRowText_KeepsTheUnshippedWorkMarkerWhenRoomIsShort(t *testing.T) {
	unshipped := pullRequestsMarker{pullRequests: manyPullRequests(7), unshipped: true}
	cases := map[int]string{
		12: "…" + strings.Repeat(iconDone, 4) + iconStatusActive + "#1234+",
		10: "…" + strings.Repeat(iconDone, 2) + iconStatusActive + "#1234+",
		8:  "…" + iconStatusActive + "#1234+",
		2:  "…" + iconStatusActive + "#1234+",
	}
	for room, marker := range cases {
		require.Equal(t, marker, stripANSI(pullRequestsRowText(unshipped, room)), "room %d", room)
	}
}

func TestRenderRow_UnshippedWorkNeverWidensTheRow(t *testing.T) {
	row := taskRow{
		task:          rowTask(),
		pullRequests:  manyPullRequests(30),
		unshippedWork: core.UnshippedWork{Commits: 4, UncommittedChanges: true},
	}

	for width := 30; width <= 140; width++ {
		line := model{}.renderRow(0, row, taskRowLayoutFor(width))

		require.Less(t, lipgloss.Width(line), width, "width %d: %q", width, stripANSI(line))
		view := rowContent(line)
		require.Contains(t, view, iconStatusActive+"#1234+", "width %d", width)
		require.LessOrEqual(t, strings.Count(view, iconDone), maxEarlierPullRequestSymbols, "width %d", width)
	}
	require.Contains(
		t,
		rowContent(model{}.renderRow(0, row, taskRowLayoutFor(34))),
		"first t…  …"+iconStatusActive+"#1234+",
	)
}

func TestUnshippedWorkDetailLine_SaysWhatNoPullRequestContains(t *testing.T) {
	cases := map[string]struct {
		work core.UnshippedWork
		line string
	}{
		"commits and changes": {
			core.UnshippedWork{Commits: 2, UncommittedChanges: true},
			"+ 2 commits and uncommitted changes not in a pull request",
		},
		"one commit": {core.UnshippedWork{Commits: 1}, "+ 1 commit not in a pull request"},
		"changes":    {core.UnshippedWork{UncommittedChanges: true}, "+ uncommitted changes not in a pull request"},
	}
	for name, tc := range cases {
		lines := unshippedWorkDetailLines(tc.work, 80)

		require.Equal(t, []string{primaryStyle.Render(tc.line)}, lines, name)
	}
}

// Words too wide for the panel wrap, lined up after the +, rather than being
// cut short.
func TestUnshippedWorkDetailLines_WrapToTheWidth(t *testing.T) {
	for _, width := range []int{42, 54} {
		lines := unshippedWorkDetailLines(core.UnshippedWork{Commits: 12, UncommittedChanges: true}, width)

		require.Greater(t, len(lines), 1, "width %d", width)
		require.True(t, strings.HasPrefix(stripANSI(lines[0]), "+ 12 commits"), "width %d", width)
		var words []string
		for index, line := range lines {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d", width)
			text := stripANSI(line)
			if index == 0 {
				text = strings.TrimPrefix(text, "+ ")
			} else {
				require.True(t, strings.HasPrefix(text, "  ") && text[2] != ' ', "width %d: %q", width, text)
			}
			words = append(words, strings.TrimSpace(text))
		}
		require.Equal(t, "12 commits and uncommitted changes not in a pull request", strings.Join(words, " "))
	}
}

func TestModel_DetailPanelSaysWhatNoPullRequestContainsUnderThePullRequests(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	m.width = 120
	m.rows[0].pullRequests = manyPullRequests(6)
	m.rows[0].unshippedWork = core.UnshippedWork{Commits: 2, UncommittedChanges: true}

	require.Equal(t, []string{
		"3 earlier",
		iconDone + " #4    merged  feat/part-4",
		iconDone + " #5    merged  feat/part-5",
		iconDone + " #6    merged  feat/part-6",
		iconStatusActive + " #1234 open    feat/now",
		"+ 2 commits and uncommitted changes not in a pull request",
	}, detailSection(m, "PULL REQUESTS"))

	// Too wide for a narrower panel, the words wrap under the pull requests.
	m.width = 50
	require.Equal(t, []string{
		"+ 2 commits and uncommitted changes",
		"  not in a pull request",
	}, detailSection(m, "PULL REQUESTS")[5:])

	m.rows[0].unshippedWork = core.UnshippedWork{}
	require.NotContains(t, strings.Join(detailSection(m, "PULL REQUESTS"), "\n"), "not in a pull request")
}

// The unshipped work loaded last stays when a refresh fails or the task list
// reloads, as the pull requests do, and changes with the next refresh.
func TestModel_UnshippedWorkSurvivesAFailedRefreshAndAReload(t *testing.T) {
	frontend := newFrontendHarness()
	m := agentsTaskModel(frontend)
	frontend.taskPullRequests = map[string]core.TaskPullRequests{"task-1": {
		PullRequests:  billingPullRequests(),
		UnshippedWork: core.UnshippedWork{Commits: 1},
	}}
	m = applyPullRequestRefresh(t, m)
	require.Equal(t, core.UnshippedWork{Commits: 1}, m.rows[0].unshippedWork)
	require.Contains(t, stripANSI(m.View().Content), billingMarker+"+")

	frontend.taskPullRequestsErr = map[string]error{"task-1": errors.New("gh: not logged in")}
	m = applyPullRequestRefresh(t, m)
	require.Equal(t, core.UnshippedWork{Commits: 1}, m.rows[0].unshippedWork)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "r"})
	m = asModel(t, next)
	next, _ = m.Update(runCmd(t, cmd))
	m = asModel(t, next)
	require.Equal(t, core.UnshippedWork{Commits: 1}, m.rows[0].unshippedWork)
	require.Contains(t, stripANSI(m.View().Content), billingMarker+"+")

	// Once the work is in a pull request, the next refresh clears the +.
	frontend.taskPullRequestsErr = nil
	frontend.taskPullRequests["task-1"] = core.TaskPullRequests{PullRequests: billingPullRequests()}
	m = applyPullRequestRefresh(t, m)
	require.Equal(t, core.UnshippedWork{}, m.rows[0].unshippedWork)
	require.NotContains(t, stripANSI(m.View().Content), billingMarker+"+")
}
