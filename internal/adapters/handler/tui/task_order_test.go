package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// minute is a moment the given number of minutes into the day the ordering
// tests describe; later minutes are more recent.
func minute(n int) time.Time {
	return time.Date(2026, time.October, 9, 9, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
}

// orderRow is a row for the task id in the repo, created at minute 0, whose
// status was phase as of minute seen; an empty phase is a task with no status.
func orderRow(id string, repo string, phase core.TaskStatusPhase, seen int) taskRow {
	row := taskRow{task: &core.Task{
		ID: id, DisplayName: id, RepoName: repo, RepoRoot: "/src/" + repo, CreatedAt: minute(0),
	}}
	if phase != "" {
		row.status = &core.TaskStatusUpdate{TaskID: id, Phase: phase, ObservedAt: minute(seen)}
	}
	return row
}

// sessionOf makes row a Child task of the task with the given ID.
func sessionOf(parentID string, row taskRow) taskRow {
	row.task.ParentID = parentID
	return row
}

// ranked freezes every row's rank from its state as the test describes it,
// the way a load that arranges the list does.
func ranked(rows []taskRow) []taskRow {
	rows = slices.Clone(rows)
	for index := range rows {
		rows[index].freezeOrder()
	}
	return rows
}

func idsOf(rows []taskRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, taskID(row.task))
	}
	return ids
}

func TestOrderRows(t *testing.T) {
	idle := core.TaskStatusPhaseWaitingForInput
	working := core.TaskStatusPhaseWorking

	// updated is a task with no status whose record was last updated at the
	// given minute.
	updated := func(id string, minutes int) taskRow {
		row := orderRow(id, "code", "", 0)
		row.task.UpdatedAt = minute(minutes)
		return row
	}
	// withActivity is row with an Activity event observed at the given minute.
	withActivity := func(row taskRow, minutes int) taskRow {
		row.activity = []core.TaskActivityEvent{
			{TaskID: taskID(row.task), ObservedAt: minute(minutes - 10)},
			{TaskID: taskID(row.task), ObservedAt: minute(minutes)},
		}
		return row
	}

	tests := map[string]struct {
		rows []taskRow
		want []string
	}{
		"a working task beats a more recently active idle one": {
			rows: []taskRow{
				orderRow("recent-idle", "code", idle, 50),
				orderRow("busy", "code", working, 5),
			},
			want: []string{"busy", "recent-idle"},
		},
		"working tasks are ordered by recency among themselves": {
			rows: []taskRow{
				orderRow("busy-old", "code", working, 5),
				orderRow("idle", "code", idle, 50),
				orderRow("busy-new", "code", working, 9),
			},
			want: []string{"busy-new", "busy-old", "idle"},
		},
		"starting and working in the background count as working, waiting and stopped do not": {
			rows: []taskRow{
				orderRow("waiting", "code", idle, 60),
				orderRow("stopped", "code", core.TaskStatusPhaseStopped, 55),
				orderRow("starting", "code", core.TaskStatusPhaseStarting, 10),
				orderRow("background", "code", core.TaskStatusPhaseWorkingInBackground, 20),
			},
			want: []string{"background", "starting", "waiting", "stopped"},
		},
		"tasks that are not working are ordered most recently active first": {
			rows: []taskRow{
				orderRow("a", "code", idle, 10),
				orderRow("b", "code", idle, 30),
				orderRow("c", "code", core.TaskStatusPhaseStopped, 20),
			},
			want: []string{"b", "c", "a"},
		},
		"a task not seen yet is placed by its record's update": {
			rows: []taskRow{
				orderRow("seen-early", "code", idle, 30),
				updated("renamed", 40),
				orderRow("seen-late", "code", idle, 50),
			},
			want: []string{"seen-late", "renamed", "seen-early"},
		},
		"a task with nothing else is placed by when it was created": {
			rows: []taskRow{
				orderRow("oldest", "code", "", 0),
				func() taskRow {
					row := orderRow("newest", "code", "", 0)
					row.task.CreatedAt = minute(15)
					return row
				}(),
				orderRow("seen", "code", idle, 5),
			},
			want: []string{"newest", "seen", "oldest"},
		},
		"an Activity event newer than the status counts as activity": {
			rows: []taskRow{
				orderRow("status-only", "code", idle, 30),
				withActivity(orderRow("prompted", "code", idle, 10), 45),
			},
			want: []string{"prompted", "status-only"},
		},
		"a working session lifts its family above the others, nested under its parent": {
			rows: []taskRow{
				orderRow("other", "code", idle, 50),
				orderRow("parent", "code", idle, 1),
				sessionOf("parent", orderRow("child", "code", working, 2)),
				orderRow("another", "code", idle, 40),
			},
			want: []string{"parent", "child", "other", "another"},
		},
		"a family is as recently active as its newest member": {
			rows: []taskRow{
				orderRow("solo", "code", idle, 30),
				orderRow("parent", "code", idle, 10),
				sessionOf("parent", orderRow("child", "code", idle, 40)),
			},
			want: []string{"parent", "child", "solo"},
		},
		"the parent stays first and its sessions follow by rank": {
			rows: []taskRow{
				orderRow("solo", "code", idle, 20),
				orderRow("parent", "code", idle, 1),
				sessionOf("parent", orderRow("child-old", "code", idle, 10)),
				sessionOf("parent", orderRow("child-busy", "code", working, 5)),
				sessionOf("parent", orderRow("child-new", "code", idle, 30)),
			},
			want: []string{"parent", "child-busy", "child-new", "child-old", "solo"},
		},
		"a session whose parent is not listed stands on its own": {
			rows: []taskRow{
				orderRow("parent", "code", idle, 50),
				sessionOf("parent", orderRow("child", "code", idle, 49)),
				sessionOf("not-listed", orderRow("orphan", "code", working, 1)),
				sessionOf("not-listed", orderRow("quiet-orphan", "code", idle, 10)),
			},
			want: []string{"orphan", "parent", "child", "quiet-orphan"},
		},
		"a session of a session stands on its own instead of being lost": {
			rows: []taskRow{
				orderRow("parent", "code", idle, 10),
				sessionOf("parent", orderRow("child", "code", idle, 20)),
				sessionOf("child", orderRow("grandchild", "code", idle, 5)),
			},
			want: []string{"parent", "child", "grandchild"},
		},
		"repo groups are ordered by their best family": {
			rows: []taskRow{
				orderRow("a-1", "alpha", idle, 50),
				orderRow("b-1", "beta", idle, 10),
				orderRow("a-2", "alpha", idle, 45),
				orderRow("b-2", "beta", working, 1),
			},
			want: []string{"b-2", "b-1", "a-1", "a-2"},
		},
		"families are ordered inside their repo group": {
			rows: []taskRow{
				orderRow("a-old", "alpha", idle, 5),
				orderRow("a-new", "alpha", idle, 20),
				orderRow("b-1", "beta", idle, 30),
			},
			want: []string{"b-1", "a-new", "a-old"},
		},
		"working groups are ordered by recency among themselves": {
			rows: []taskRow{
				orderRow("a-1", "alpha", working, 5),
				orderRow("a-2", "alpha", idle, 90),
				orderRow("b-1", "beta", working, 9),
			},
			want: []string{"b-1", "a-1", "a-2"},
		},
		"a session joins its parent's repo group and lifts it": {
			rows: []taskRow{
				orderRow("a-1", "alpha", idle, 50),
				orderRow("b-1", "beta", idle, 10),
				sessionOf("b-1", orderRow("b-2", "beta", working, 11)),
			},
			want: []string{"b-1", "b-2", "a-1"},
		},
		"nothing listed gives nothing": {
			rows: nil,
			want: []string{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, idsOf(orderRows(ranked(tt.rows))))
		})
	}
}

func TestOrderRows_TiesAreBrokenByCreationThenIDWhateverTheInputOrder(t *testing.T) {
	idle := core.TaskStatusPhaseWaitingForInput
	older := orderRow("older", "code", idle, 10)
	older.task.CreatedAt = minute(-5)
	rows := ranked([]taskRow{
		orderRow("tie-b", "code", idle, 10),
		orderRow("tie-a", "code", idle, 10),
		older,
		orderRow("tie-c", "code", idle, 10),
	})
	want := []string{"older", "tie-a", "tie-b", "tie-c"}

	permute(rows, func(permuted []taskRow) {
		require.Equal(t, want, idsOf(orderRows(permuted)), "from %v", idsOf(permuted))
	})
}

func TestOrderRows_AFamilyKeepsItsPlaceWhateverTheInputOrder(t *testing.T) {
	idle := core.TaskStatusPhaseWaitingForInput
	rows := ranked([]taskRow{
		orderRow("parent", "code", idle, 10),
		sessionOf("parent", orderRow("child-a", "code", idle, 30)),
		sessionOf("parent", orderRow("child-b", "code", idle, 20)),
		orderRow("solo", "other", idle, 25),
	})
	want := []string{"parent", "child-a", "child-b", "solo"}

	permute(rows, func(permuted []taskRow) {
		require.Equal(t, want, idsOf(orderRows(permuted)), "from %v", idsOf(permuted))
	})
}

func TestOrderRows_ReturnsANewSliceAndLeavesItsInputAlone(t *testing.T) {
	idle := core.TaskStatusPhaseWaitingForInput
	rows := ranked([]taskRow{
		orderRow("old", "code", idle, 1),
		orderRow("new", "code", idle, 9),
	})

	ordered := orderRows(rows)

	require.Equal(t, []string{"new", "old"}, idsOf(ordered))
	require.Equal(t, []string{"old", "new"}, idsOf(rows))
}

func TestOrderRows_GoesByTheFrozenRankNotByTheLiveState(t *testing.T) {
	idle := core.TaskStatusPhaseWaitingForInput
	rows := ranked([]taskRow{
		orderRow("older", "code", idle, 10),
		orderRow("newer", "code", idle, 20),
	})
	require.Equal(t, []string{"newer", "older"}, idsOf(orderRows(rows)))

	// A live status arrives for the lower row, and an Activity event for the
	// other: both show on the rows, neither moves them.
	rows[0].status = &core.TaskStatusUpdate{TaskID: "older", Phase: core.TaskStatusPhaseWorking, ObservedAt: minute(99)}
	rows[1].activity = []core.TaskActivityEvent{{TaskID: "newer", ObservedAt: minute(5)}}
	require.Equal(t, []string{"newer", "older"}, idsOf(orderRows(rows)))

	// Freezing the rank again, as a load that arranges the list does, takes
	// the live state into account.
	rows[0].freezeOrder()
	require.Equal(t, []string{"older", "newer"}, idsOf(orderRows(rows)))
}

func TestOrderRows_RowsAlreadyListedKeepTheirOrderWhenOneIsAdded(t *testing.T) {
	idle := core.TaskStatusPhaseWaitingForInput
	listed := orderRows(ranked([]taskRow{
		orderRow("parent", "code", idle, 30),
		sessionOf("parent", orderRow("child", "code", idle, 5)),
		orderRow("solo", "code", idle, 20),
		orderRow("elsewhere", "other", idle, 10),
	}))
	require.Equal(t, []string{"parent", "child", "solo", "elsewhere"}, idsOf(listed))

	// A row shows a live status it was not ranked by.
	listed[2].status = &core.TaskStatusUpdate{
		TaskID:     "solo",
		Phase:      core.TaskStatusPhaseWorking,
		ObservedAt: minute(90),
	}
	require.Equal(t, []string{"parent", "child", "solo", "elsewhere"}, idsOf(orderRows(listed)))

	// Rows new to the list are ranked once, by their record: a new Child task
	// is the newest and lifts its family, a new task is placed by its age.
	newChild := sessionOf("solo", orderRow("new-child", "code", "", 0))
	newChild.task.CreatedAt = minute(60)
	newChild.freezeOrder()
	newer := orderRow("newer", "third", "", 0)
	newer.task.CreatedAt = minute(70)
	newer.freezeOrder()

	arranged := orderRows(slices.Concat(listed, []taskRow{newChild, newer}))

	require.Equal(t, []string{"newer", "solo", "new-child", "parent", "child", "elsewhere"}, idsOf(arranged))
}

// permute calls check with every ordering of rows.
func permute(rows []taskRow, check func([]taskRow)) {
	var recurse func(prefix []taskRow, rest []taskRow)
	recurse = func(prefix []taskRow, rest []taskRow) {
		if len(rest) == 0 {
			check(append([]taskRow(nil), prefix...))
			return
		}
		for index := range rest {
			remaining := append(append([]taskRow(nil), rest[:index]...), rest[index+1:]...)
			recurse(append(prefix, rest[index]), remaining)
		}
	}
	recurse(nil, rows)
}

// orderTasksFixture is three tasks of one folder, created an hour apart, so
// that with no status the newest is listed first.
func orderTasksFixture() []*core.Task {
	task := func(id string, name string, created int) *core.Task {
		return &core.Task{
			ID: id, DisplayName: name + " task", BranchName: "feat/" + name, RepoName: "code", RepoRoot: "/src/code",
			WorktreePath: "/src/code", WorkspaceKind: core.WorkspaceKindFolder, Provider: core.ProviderCodex,
			CreatedAt: minute(created),
		}
	}
	return []*core.Task{task("task-1", "first", 0), task("task-2", "second", 60), task("task-3", "third", 120)}
}

func statusMsg(taskID string, phase core.TaskStatusPhase, seen int) taskStatusUpdatedMsg {
	return taskStatusUpdatedMsg{
		taskID:  taskID,
		updates: make(chan core.TaskStatusUpdate),
		update: core.TaskStatusUpdate{
			TaskID: taskID, Provider: core.ProviderCodex, Phase: phase, ObservedAt: minute(seen),
		},
	}
}

func updateModel(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(model)
	require.True(t, ok)
	return got
}

// listedAt reports where in the view the text first appears, or -1.
func listedAt(m model, text string) int {
	return strings.Index(stripANSI(m.View().Content), text)
}

// settleDeadline delivers the deadline of the load that last arranged the list.
func settleDeadline(t *testing.T, m model) model {
	t.Helper()
	return updateModel(t, m, orderSettleMsg{seq: m.settle.seq})
}

func TestModel_OpeningShowsTheListAtOnceThenSettlesWhenTheFirstStatusesHaveArrived(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m, loadMsg := initModel(t, newModel(frontend.mock, "/src/code", ""))
	next, cmd := m.Update(loadMsg)
	m, _ = next.(model)

	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m), "by the records, at once")
	require.Len(t, m.settle.pending, 3)
	tick := requireMsgType[orderSettleMsg](t, runBatchCmd(t, cmd))
	require.Equal(t, m.settle.seq, tick.seq, "the load schedules the deadline of its own wait")
	m = pressKeys(t, m, keyText("j"))
	require.Equal(t, "task-2", m.selectedTaskID())

	// Statuses arrive one task at a time: the rows show them and stay put.
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 200))
	m = updateModel(t, m, statusMsg("task-2", core.TaskStatusPhaseWaitingForInput, 300))
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))
	require.Len(t, m.settle.pending, 1)

	// The last one settles the order, once: working first, then the task
	// last seen most recently. The selection stays on its task.
	m = updateModel(t, m, statusMsg("task-3", core.TaskStatusPhaseWaitingForInput, 130))

	require.Equal(t, []string{"task-1", "task-2", "task-3"}, rowIDs(m))
	require.Equal(t, "task-2", m.selectedTaskID())
	require.Equal(t, 1, m.selected)
	require.Empty(t, m.settle.pending)

	// Nothing moves the rows after that, whatever arrives.
	m = updateModel(t, m, statusMsg("task-3", core.TaskStatusPhaseWorking, 500))
	m = updateModel(t, m, taskActivityLoadedMsg{
		taskID:   "task-2",
		activity: []core.TaskActivityEvent{{TaskID: "task-2", ObservedAt: minute(600)}},
	})
	m = settleDeadline(t, m)
	require.Equal(t, []string{"task-1", "task-2", "task-3"}, rowIDs(m))
	require.Equal(t, "task-2", m.selectedTaskID())
	require.Empty(t, frontend.latestTaskStatusCalls, "statuses come from the subscriptions, never a read")
}

func TestModel_TheSettleDeadlineSettlesWithTheStatusesThatHaveArrived(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	m = pressKeys(t, m, keyText("j"))
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 200))
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m), "two tasks have said nothing yet")
	require.Len(t, m.settle.pending, 2)

	m = settleDeadline(t, m)

	require.Equal(t, []string{"task-1", "task-3", "task-2"}, rowIDs(m))
	require.Equal(t, "task-2", m.selectedTaskID())
	require.Equal(t, 2, m.selected)
	require.Empty(t, m.settle.pending)
	require.Empty(t, frontend.latestTaskStatusCalls)
}

func TestModel_AnEndedSubscriptionCountsAsAStatusThatWillNotCome(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 200))
	m = updateModel(t, m, taskStatusSubscriptionReadyMsg{taskID: "task-2", err: errors.New("no daemon")})
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))

	m = updateModel(t, m, taskStatusSubscriptionClosedMsg{taskID: "task-3"})

	require.Equal(t, []string{"task-1", "task-3", "task-2"}, rowIDs(m))
	require.Empty(t, m.settle.pending)
}

func TestModel_ADeadlineFromASupersededLoadDoesNothing(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	staleSeq := m.settle.seq
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 200))

	// Another refresh arranges the list again, replacing the wait: the rows
	// carry task-1's status, and the other two have said nothing.
	m = press(t, m, keyText("r"))
	require.Greater(t, m.settle.seq, staleSeq)
	require.Equal(t, []string{"task-1", "task-3", "task-2"}, rowIDs(m), "task-1 is first at once")
	require.Len(t, m.settle.pending, 2)
	m = updateModel(t, m, statusMsg("task-2", core.TaskStatusPhaseWorking, 250))
	require.Equal(t, []string{"task-1", "task-3", "task-2"}, rowIDs(m))

	m = updateModel(t, m, orderSettleMsg{seq: staleSeq})

	require.Equal(t, []string{"task-1", "task-3", "task-2"}, rowIDs(m), "the old deadline settles nothing")
	require.Len(t, m.settle.pending, 1)

	m = settleDeadline(t, m)

	require.Equal(t, []string{"task-2", "task-1", "task-3"}, rowIDs(m), "the current one does")
	require.Empty(t, m.settle.pending)
}

func TestModel_RefreshWithTheStatusesAlreadyCarriedReordersAtOnceAndWaitsForNothing(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWaitingForInput, 100))
	m = updateModel(t, m, statusMsg("task-2", core.TaskStatusPhaseWaitingForInput, 110))
	m = updateModel(t, m, statusMsg("task-3", core.TaskStatusPhaseWaitingForInput, 130))
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))
	require.Empty(t, m.settle.pending)
	m = pressKeys(t, m, keyText("j"))
	require.Equal(t, "task-2", m.selectedTaskID())

	// Two tasks start working while the rows hold still; then the refresh.
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 400))
	m = updateModel(t, m, statusMsg("task-3", core.TaskStatusPhaseWorking, 500))
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))
	next, cmd := m.Update(keyText("r"))
	m, _ = next.(model)
	loaded := requireMsgType[tasksLoadedMsg](t, []tea.Msg{runCmd(t, cmd)})
	next, follow := m.Update(loaded)
	m, _ = next.(model)

	require.Equal(t, []string{"task-3", "task-1", "task-2"}, rowIDs(m), "arranged at once, by the live state")
	require.Equal(t, "task-2", m.selectedTaskID())
	require.Empty(t, m.settle.pending, "there is no status to wait for")
	for _, msg := range runBatchCmd(t, follow) {
		require.NotEqual(t, orderSettleMsg{seq: m.settle.seq}, msg, "so no deadline is scheduled")
	}
	require.Empty(t, frontend.latestTaskStatusCalls)
}

func TestModel_ALiveUpdateMovesNothingWhileNoSettleIsPending(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	m.detailsHidden = false
	m.width, m.height = 100, 40
	m = settleDeadline(t, m)
	m = pressKeys(t, m, keyText("j"))
	require.Equal(t, "task-2", m.selectedTaskID())

	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 200))
	m = updateModel(t, m, statusMsg("task-2", core.TaskStatusPhaseWorking, 210))
	m = updateModel(t, m, taskActivityLoadedMsg{
		taskID:   "task-1",
		activity: []core.TaskActivityEvent{{TaskID: "task-1", ObservedAt: minute(300), Text: "carry on"}},
	})

	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))
	require.Equal(t, "task-2", m.selectedTaskID())
	require.Equal(t, 1, m.selected)
	require.Equal(t, core.TaskStatusPhaseWorking, m.rows[2].status.Phase, "the row shows the live status")
	require.Len(t, m.rows[2].activity, 1)
	require.Contains(t, stripANSI(m.View().Content), "feat/second")
	require.Less(t, listedAt(m, "third task"), listedAt(m, "second task"))
	require.Less(t, listedAt(m, "second task"), listedAt(m, "first task"))
}

func TestModel_AReloadThatIsNotARefreshKeepsRowsInPlaceAndPutsANewTaskOnTop(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	m = pressKeys(t, m, keyText("j"))
	m = settleDeadline(t, m)
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 200))
	created := &core.Task{ID: "task-4", DisplayName: "new task", RepoName: "code", RepoRoot: "/src/code",
		WorkspaceKind: core.WorkspaceKindFolder, CreatedAt: minute(400), UpdatedAt: minute(400)}
	frontend.listTasks = append(frontend.listTasks, created)

	// What the reload after a create, a cleanup, a shelving or an adoption
	// delivers, and what the created task's own event does.
	reloaded := updateModel(t, m, tasksLoadedMsg{tasks: frontend.listTasks})
	upserted := updateModel(t, m, taskCreatedMsg{task: created})

	for name, got := range map[string]model{"reload": reloaded, "created": upserted} {
		require.Equal(t, []string{"task-4", "task-3", "task-2", "task-1"}, rowIDs(got), name)
		require.Equal(t, core.TaskStatusPhaseWorking, got.rows[3].status.Phase, "%s: the status is kept", name)
		require.Empty(t, got.settle.pending, "%s: no wait begins", name)
	}
	require.Equal(t, "task-2", reloaded.selectedTaskID(), "the reload leaves the selection on its task")
	require.Equal(t, "task-4", upserted.selectedTaskID(), "the created task is selected")
	require.Empty(t, frontend.latestTaskStatusCalls)
}

func TestModel_AReloadThatIsNotARefreshKeepsTheRanksTheLastArrangementGave(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 200))
	m = settleDeadline(t, m)
	require.Equal(t, []string{"task-1", "task-3", "task-2"}, rowIDs(m))
	require.Equal(t, "task-3", m.selectedTaskID())
	frontend.listTasks = append(frontend.listTasks, &core.Task{
		ID: "task-4", DisplayName: "new task", RepoName: "code", RepoRoot: "/src/code",
		WorkspaceKind: core.WorkspaceKindFolder, CreatedAt: minute(400), UpdatedAt: minute(400),
	})

	// The working task, whose status the reload does not return, stays first;
	// the new task is the newest of the rest.
	m = updateModel(t, m, tasksLoadedMsg{tasks: frontend.listTasks})

	require.Equal(t, []string{"task-1", "task-4", "task-3", "task-2"}, rowIDs(m))
	require.Equal(t, "task-3", m.selectedTaskID())
}

func TestModel_ANewChildTaskLiftsItsFamilyAndThenHoldsStill(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	m = settleDeadline(t, m)
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))

	child := &core.Task{ID: "task-4", DisplayName: "first task, again", RepoName: "code", RepoRoot: "/src/code",
		WorkspaceKind: core.WorkspaceKindFolder, ParentID: "task-1", CreatedAt: minute(400), UpdatedAt: minute(400)}
	m = updateModel(t, m, taskCreatedMsg{task: child})

	require.Equal(t, []string{"task-1", "task-4", "task-3", "task-2"}, rowIDs(m))

	m = updateModel(t, m, statusMsg("task-2", core.TaskStatusPhaseWorking, 500))
	require.Equal(t, []string{"task-1", "task-4", "task-3", "task-2"}, rowIDs(m))
}

func TestModel_ShowingAllFoldersArrangesTheListAgainAndSettlesOnTheNewTasksStatuses(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "far-1", DisplayName: "far task", RepoName: "code", RepoRoot: "/src/work/code",
			WorktreePath: "/src/work/code", WorkspaceKind: core.WorkspaceKindFolder, Provider: core.ProviderCodex,
			CreatedAt: minute(0)},
		{ID: "here-1", DisplayName: "here task", RepoName: "code", RepoRoot: "/src/project/code",
			WorktreePath: "/src/project/code", WorkspaceKind: core.WorkspaceKindFolder, Provider: core.ProviderCodex,
			CreatedAt: minute(10)},
		{ID: "here-2", DisplayName: "here too", RepoName: "api", RepoRoot: "/src/project/code/api",
			WorktreePath: "/src/project/code/api_fix", Provider: core.ProviderCodex, CreatedAt: minute(20)},
	}
	m := dashboardIn(t, frontend, "/src/project/code")
	require.Equal(t, []string{"here-2", "here-1"}, rowIDs(m))
	m = updateModel(t, m, statusMsg("here-1", core.TaskStatusPhaseWorking, 100))
	m = updateModel(t, m, statusMsg("here-2", core.TaskStatusPhaseWaitingForInput, 120))
	require.Equal(t, []string{"here-1", "here-2"}, rowIDs(m), "settled by the statuses")
	m = pressKeys(t, m, keyText("j"))
	require.Equal(t, "here-2", m.selectedTaskID())

	m = press(t, m, keyText("f"))

	require.Equal(t, []string{"here-1", "here-2", "far-1"}, rowIDs(m), "at once; the far task has no status yet")
	require.Equal(t, map[string]bool{"far-1": true}, m.settle.pending, "and only it is waited for")

	m = updateModel(t, m, statusMsg("far-1", core.TaskStatusPhaseWorking, 500))

	require.Equal(t, []string{"far-1", "here-1", "here-2"}, rowIDs(m), "it works more recently: first")
	require.Equal(t, "here-2", m.selectedTaskID())
	require.Empty(t, frontend.latestTaskStatusCalls)
}

func TestModel_TheShelfIsArrangedAgainLikeTheCurrentList(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	for _, task := range frontend.listTasks {
		task.ShelvedAt = minute(1000)
	}
	frontend.listTasks = append(frontend.listTasks, &core.Task{
		ID: "task-4", DisplayName: "current task", RepoName: "code", RepoRoot: "/src/code",
		WorktreePath: "/src/code", WorkspaceKind: core.WorkspaceKindFolder, CreatedAt: minute(500),
	})
	m := dashboardIn(t, frontend, "/src/code")
	require.Equal(t, []string{"task-4"}, rowIDs(m))
	m = updateModel(t, m, statusMsg("task-4", core.TaskStatusPhaseWaitingForInput, 600))

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	require.True(t, m.showShelf)
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m), "at once, by the records")
	require.Len(t, m.settle.pending, 3, "the shelf's tasks are waited for, not the current list's")
	m = pressKeys(t, m, keyText("j"), keyText("j"))
	require.Equal(t, "task-1", m.selectedTaskID())

	m = updateModel(t, m, statusMsg("task-2", core.TaskStatusPhaseWorking, 1200))
	m = updateModel(t, m, statusMsg("task-3", core.TaskStatusPhaseWaitingForInput, 100))
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWaitingForInput, 900))

	require.Equal(t, []string{"task-2", "task-1", "task-3"}, rowIDs(m))
	require.Equal(t, "task-1", m.selectedTaskID())
	require.Empty(t, frontend.latestTaskStatusCalls)
}

func TestModel_OpeningATaskFromTheShelfArrangesTheCurrentListAgain(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	frontend.listTasks[0].ShelvedAt = minute(1000)
	m := dashboardIn(t, frontend, "/src/code")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, []string{"task-1"}, rowIDs(m))
	seq := m.settle.seq

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	require.False(t, m.showShelf)
	require.Greater(t, m.settle.seq, seq, "the current list is a new view, arranged afresh")
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))
	m = updateModel(t, m, statusMsg("task-2", core.TaskStatusPhaseWorking, 1200))
	m = updateModel(t, m, statusMsg("task-3", core.TaskStatusPhaseWaitingForInput, 130))
	m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWaitingForInput, 140))
	require.Equal(t, []string{"task-2", "task-1", "task-3"}, rowIDs(m))
}

func TestModel_UpsertTaskRowKeepsAListedRowsPlaceAndRanksANewOneOnce(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")
	require.Equal(t, []string{"task-3", "task-2", "task-1"}, rowIDs(m))

	created := &core.Task{ID: "task-4", DisplayName: "new task", RepoName: "code", RepoRoot: "/src/code",
		CreatedAt: minute(400), UpdatedAt: minute(400)}
	require.Equal(t, 0, m.upsertTaskRow(created), "a new task is the most recently active")
	require.Equal(t, []string{"task-4", "task-3", "task-2", "task-1"}, rowIDs(m))
	require.Equal(t, "task-3", m.selectedTaskID(), "the selection stays on its task")

	touched := *m.rows[3].task
	touched.UpdatedAt = minute(500)
	require.Equal(t, 3, m.upsertTaskRow(&touched), "a changed record does not move its row")
	require.Equal(t, []string{"task-4", "task-3", "task-2", "task-1"}, rowIDs(m))
	require.Equal(t, minute(500), m.rows[3].task.UpdatedAt, "though it replaces the record")

	require.Equal(t, -1, m.upsertTaskRow(&core.Task{}), "a task without an ID is not listed")
	require.Len(t, m.rows, 4)
}

func TestModel_RemovingATaskKeepsTheOrderTheNestingAndTheSelection(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = orderTasksFixture()
	frontend.listTasks = append(frontend.listTasks, &core.Task{
		ID: "task-4", DisplayName: "third task, again", RepoName: "code", RepoRoot: "/src/code",
		WorkspaceKind: core.WorkspaceKindFolder, ParentID: "task-3", CreatedAt: minute(130),
	})
	m := dashboardIn(t, frontend, "/src/code")
	require.Equal(t, []string{"task-3", "task-4", "task-2", "task-1"}, rowIDs(m))
	m = pressKeys(t, m, keyText("j"), keyText("j"))
	require.Equal(t, "task-2", m.selectedTaskID())

	m.removeTaskRow("task-3")
	m.clampSelection()
	require.Equal(t, []string{"task-4", "task-2", "task-1"}, rowIDs(m))
	require.Equal(t, "task-2", m.selectedTaskID(), "the task above going does not move the selection")

	m.removeTaskRow("task-2")
	m.clampSelection()
	require.Equal(t, []string{"task-4", "task-1"}, rowIDs(m))
	require.Equal(t, 1, m.selected, "the selection stays at its place when its task is the one removed")
}

// The open confirmations, renames and provider switches name the selected
// task when they are submitted, so a settle or a refresh that arranges the
// list while one is open must leave them on the task they were opened for.
func TestModel_AReorderWhileAModeIsOpenLeavesItOnItsTask(t *testing.T) {
	setup := func(t *testing.T) (*frontendHarness, model) {
		t.Helper()
		frontend := newFrontendHarness()
		frontend.providerSetup = &core.ProviderSetup{
			Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
			Default:    core.ProviderCodex,
		}
		frontend.listTasks = orderTasksFixture()
		m := dashboardIn(t, frontend, "/src/code")
		m = pressKeys(t, m, keyText("j"))
		require.Equal(t, "task-2", m.selectedTaskID())
		return frontend, m
	}
	// reorder settles the open wait with the lowest task working, which
	// shifts the open one down a place.
	reorder := func(t *testing.T, m model) model {
		t.Helper()
		m = updateModel(t, m, statusMsg("task-1", core.TaskStatusPhaseWorking, 200))
		m = settleDeadline(t, m)
		require.Equal(t, []string{"task-1", "task-3", "task-2"}, rowIDs(m))
		return m
	}

	t.Run("cleanup", func(t *testing.T) {
		frontend, m := setup(t)
		m = pressKeys(t, m, keyText("x"))
		require.Equal(t, modeCleanupConfirm, m.mode)

		m = reorder(t, m)
		require.Contains(t, stripANSI(m.View().Content), "second task")
		_, cmd := m.Update(keyText("y"))
		runBatchCmd(t, cmd)

		require.Equal(t, []string{"task-2"}, frontend.deleteTaskIDs)
	})

	t.Run("rename", func(t *testing.T) {
		frontend, m := setup(t)
		m = pressKeys(t, m, keyText("e"))
		require.Equal(t, modeRenameTask, m.mode)

		m = reorder(t, m)
		m = pressKeys(t, m, typed("!")...)
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runBatchCmd(t, cmd)

		require.Equal(t, map[string]string{"task-2": "second task!"}, frontend.renamedTasks)
	})

	t.Run("switch provider", func(t *testing.T) {
		frontend, m := setup(t)
		m = pressKeys(t, m, keyText("p"))
		require.Equal(t, modeSwitchProvider, m.mode)

		m = reorder(t, m)
		_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runBatchCmd(t, cmd)

		require.Equal(t, "task-2", frontend.switchedTaskID)
		require.Equal(t, core.ProviderClaude, frontend.switchedProvider)
	})
}
