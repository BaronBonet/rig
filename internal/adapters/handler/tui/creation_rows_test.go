package tui

import (
	"errors"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// repoTask is a ready task in /tmp/repo.
func repoTask(id string, name string) *core.Task {
	return &core.Task{ID: id, DisplayName: name, RepoRoot: "/tmp/repo", RepoName: "repo", Provider: core.ProviderCodex}
}

// creatingRepoTask is a task in /tmp/repo whose creation is under way.
func creatingRepoTask(id string, name string) *core.Task {
	task := repoTask(id, name)
	task.CreationStatus = core.TaskCreationStatusCreating
	return task
}

func creationProgress(id int, step core.TaskCreateProgressStep, task *core.Task) tea.Msg {
	return taskCreateEventMsg{creation: id, event: core.TaskCreateEvent{
		Progress: &core.TaskCreateProgressEvent{Step: step, Task: task},
	}}
}

func update(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return asModel(t, next)
}

func TestModel_CreationRowShowsItsStepThenItsTasksName(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{repoTask("task-1", "first task")}
	m := newLoadedModel(frontend)
	id := m.addCreationRow("/tmp/repo", core.Task{Provider: core.ProviderCodex, Prompt: "fix the retry loop"})
	layout := taskRowLayoutFor(100)

	// Until it is named, the row shows only its step, under its repository.
	require.Equal(t, 1, m.selected)
	require.Contains(t, rowContent(m.renderRow(1, m.rows[1], layout)), "creating task")
	m = update(t, m, creationProgress(id, core.TaskCreateProgressSuggestingName, nil))
	require.Contains(t, rowContent(m.renderRow(1, m.rows[1], layout)), "suggesting name")

	m = update(t, m, creationProgress(
		id, core.TaskCreateProgressCreatingWorktree, creatingRepoTask("task-2", "retry loop fix"),
	))

	row := m.selectedRow()
	require.Equal(t, "task-2", row.task.ID)
	require.NotNil(t, row.creating)
	require.Contains(t, rowContent(m.renderRow(1, *row, layout)), "retry loop fix  creating worktree")
	require.Contains(t, stripANSI(m.selectedTaskDetailView()), "Creating worktree")
}

func TestModel_TwoCreationsFinishIndependently(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{repoTask("task-1", "first task")}
	m := newLoadedModel(frontend)
	first := m.addCreationRow("/tmp/repo", core.Task{})
	second := m.addCreationRow("/tmp/repo", core.Task{})
	require.Len(t, m.rows, 3)
	require.Equal(t, 2, m.selected)

	m = update(t, m, taskCreateEventMsg{creation: second, event: core.TaskCreateEvent{
		Task: repoTask("task-3", "second task"),
	}})

	require.Equal(t, -1, m.creationRow(second))
	require.Equal(t, "task-3", m.rows[2].task.ID)
	require.Nil(t, m.rows[2].creating)
	require.Equal(t, first, m.rows[1].creating.id)
	require.Equal(t, "task-3", m.selectedRow().task.ID)

	m = update(t, m, taskCreateEventMsg{creation: first, event: core.TaskCreateEvent{Err: errors.New("boom")}})

	require.ErrorContains(t, m.err, "boom")
	require.Len(t, m.rows, 2)
	require.Equal(t, "task-3", m.selectedRow().task.ID)
	require.False(t, m.anyWorking())
}

func TestModel_ReloadKeepsCreationRows(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{repoTask("task-1", "first task")}
	m := newLoadedModel(frontend)
	unnamed := m.addCreationRow("/tmp/repo", core.Task{})
	named := m.addCreationRow("/tmp/repo", core.Task{})
	m.advanceCreation(named, core.TaskCreateProgressEvent{
		Step: core.TaskCreateProgressCreatingWorktree,
		Task: creatingRepoTask("task-2", "named task"),
	})

	m = update(t, m, tasksLoadedMsg{tasks: []*core.Task{
		repoTask("task-1", "first task"),
		creatingRepoTask("task-2", "named task"),
	}})

	// The named creation goes on on its task's reloaded row, and the unnamed
	// one keeps its own row.
	require.Len(t, m.rows, 3)
	require.Equal(t, "task-2", m.rows[1].task.ID)
	require.Equal(t, named, m.rows[1].creating.id)
	require.Equal(t, core.TaskCreateProgressCreatingWorktree, m.rows[1].creating.step)
	require.Equal(t, unnamed, m.rows[2].creating.id)
	require.Equal(t, 1, m.selected)
}

// A reload can list a new task before the progress that names its row.
func TestModel_CreationTakesOverTheRowAReloadGaveItsTask(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{repoTask("task-1", "first task")}
	m := newLoadedModel(frontend)
	id := m.addCreationRow("/tmp/repo", core.Task{})
	m = update(t, m, tasksLoadedMsg{tasks: []*core.Task{
		repoTask("task-1", "first task"),
		creatingRepoTask("task-2", "new task"),
	}})
	require.Len(t, m.rows, 3)

	m = update(
		t,
		m,
		creationProgress(id, core.TaskCreateProgressCreatingWorktree, creatingRepoTask("task-2", "new task")),
	)

	require.Len(t, m.rows, 2)
	require.Equal(t, "task-2", m.rows[1].task.ID)
	require.Equal(t, id, m.rows[1].creating.id)
	require.Equal(t, 1, m.selected)
}

func TestModel_CreationRowGoesUnderTheRepositoryHoldingItsCwd(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoRoot: "/src/app", RepoName: "app", WorktreePath: "/src/app_feature"},
		{ID: "task-2", RepoRoot: "/src/api", RepoName: "api"},
	}
	m := newLoadedModel(frontend)

	for cwd, want := range map[string][2]string{
		"/src/app":           {"/src/app", "app"},
		"/src/app/internal":  {"/src/app", "app"},
		"/src/app_feature/x": {"/src/app", "app"},
		"/src/api":           {"/src/api", "api"},
		"/src/apple":         {"/src/apple", "apple"},
	} {
		root, name := m.creationRepo(cwd)
		require.Equal(t, want, [2]string{root, name}, cwd)
	}
}

// A retry that ends without a task, refused or cut off, leaves the failed
// task's row as it was, ready to retry again.
func TestModel_RetryEndingWithoutATaskKeepsTheFailedRow(t *testing.T) {
	for name, end := range map[string]func(id int) tea.Msg{
		"stream refused": func(id int) tea.Msg {
			return taskCreateStreamStartFailedMsg{creation: id, err: errors.New("operation in progress")}
		},
		"error only": func(id int) tea.Msg {
			return taskCreateEventMsg{creation: id, event: core.TaskCreateEvent{Err: errors.New("operation in progress")}}
		},
		"stream closed": func(id int) tea.Msg { return taskCreateStreamClosedMsg{creation: id} },
	} {
		t.Run(name, func(t *testing.T) {
			failed := repoTask("task-2", "failed task")
			failed.CreationStatus = core.TaskCreationStatusFailed
			failed.CreationStep = core.TaskCreateProgressPreparingWorkspace
			frontend := newFrontendHarness()
			frontend.listTasks = []*core.Task{repoTask("task-1", "first task"), failed}
			m := newLoadedModel(frontend)
			require.True(t, m.selectRow("task-2"))

			m = update(t, m, tea.KeyPressMsg{Text: "R"})
			require.NotNil(t, m.selectedRow().creating)
			m = update(t, m, end(m.selectedRow().creating.id))

			require.Error(t, m.err)
			require.Len(t, m.rows, 2)
			row := m.selectedRow()
			require.Equal(t, "task-2", row.task.ID)
			require.Nil(t, row.creating)
			symbol, _ := m.taskRowSymbol(*row)
			require.Equal(t, iconFailed, symbol)
		})
	}
}

func TestModel_RetryWaitsWhileASessionOpens(t *testing.T) {
	failed := repoTask("task-1", "failed task")
	failed.CreationStatus = core.TaskCreationStatusFailed
	failed.CreationStep = core.TaskCreateProgressPreparingWorkspace
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{failed}
	m := newLoadedModel(frontend)
	m.opening = true

	next, cmd := m.Update(tea.KeyPressMsg{Text: "R"})

	require.Nil(t, cmd)
	require.Nil(t, asModel(t, next).rows[0].creating)
	require.Zero(t, frontend.retryTaskStreamCalls)
}
