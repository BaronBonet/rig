package cli

import (
	"context"
	"errors"
	"testing"

	"agent/internal/core"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestModelUpdate_JAndKChangeSelectedRow(t *testing.T) {
	m := newLoadedTUIModel(t, &fakeTUIService{}, tuiTask("task-one"), tuiTask("task-two"), tuiTask("task-three"))

	m, _ = updateTUIModel(t, m, keyRunes("j"))
	require.Equal(t, 1, m.selected)

	m, _ = updateTUIModel(t, m, keyRunes("k"))
	require.Equal(t, 0, m.selected)
}

func TestModelUpdate_GAndGJumpToBounds(t *testing.T) {
	m := newLoadedTUIModel(t, &fakeTUIService{}, tuiTask("task-one"), tuiTask("task-two"), tuiTask("task-three"))

	m, _ = updateTUIModel(t, m, keyRunes("G"))
	require.Equal(t, 2, m.selected)

	m, _ = updateTUIModel(t, m, keyRunes("g"))
	require.Equal(t, 0, m.selected)
}

func TestModelUpdate_XEntersConfirmationMode(t *testing.T) {
	m := newLoadedTUIModel(t, &fakeTUIService{}, tuiTask("task-one"))

	m, cmd := updateTUIModel(t, m, keyRunes("x"))
	require.True(t, m.confirming)
	require.Nil(t, cmd)
}

func TestModelUpdate_ConfirmationCancelKeysDismissWithoutQuitting(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "n", key: keyRunes("n")},
		{name: "escape", key: tea.KeyMsg{Type: tea.KeyEsc}},
		{name: "q", key: keyRunes("q")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLoadedTUIModel(t, &fakeTUIService{}, tuiTask("task-one"))
			m, _ = updateTUIModel(t, m, keyRunes("x"))

			m, cmd := updateTUIModel(t, m, tt.key)
			require.False(t, m.confirming)
			require.Nil(t, cmd)
		})
	}
}

func TestModelUpdate_YDispatchesCleanupAndRefreshesList(t *testing.T) {
	service := &fakeTUIService{
		tasks: []*core.Task{tuiTask("task-one"), tuiTask("task-two")},
	}
	m := newLoadedTUIModel(t, service, service.tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("j"))
	m, _ = updateTUIModel(t, m, keyRunes("x"))

	m, cleanupCmd := updateTUIModel(t, m, keyRunes("y"))
	require.False(t, m.confirming)
	require.True(t, m.busy)
	require.NotNil(t, cleanupCmd)

	msg := cleanupCmd()
	_, ok := msg.(cleanupFinishedMsg)
	require.True(t, ok)
	require.Equal(t, "task-two", service.deletedIDOrSlug)

	m, refreshCmd := updateTUIModel(t, m, msg)
	require.True(t, m.busy)
	require.NotNil(t, refreshCmd)

	refreshMsg := refreshCmd()
	_, ok = refreshMsg.(tasksLoadedMsg)
	require.True(t, ok)
	require.Equal(t, 1, service.listCalls)
}

func TestModelUpdate_RTriggersRefreshCommand(t *testing.T) {
	service := &fakeTUIService{
		tasks: []*core.Task{tuiTask("task-one")},
	}
	m := newLoadedTUIModel(t, service, service.tasks...)

	m, cmd := updateTUIModel(t, m, keyRunes("r"))
	require.NotNil(t, cmd)
	require.True(t, m.busy)
	require.True(t, m.loading)

	msg := cmd()
	_, ok := msg.(tasksLoadedMsg)
	require.True(t, ok)
	require.Equal(t, 1, service.listCalls)
}

func TestModelUpdate_QQuitsFromNormalMode(t *testing.T) {
	m := newLoadedTUIModel(t, &fakeTUIService{}, tuiTask("task-one"))

	_, cmd := updateTUIModel(t, m, keyRunes("q"))
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	require.True(t, ok)
}

func TestModelUpdate_BusyStateBlocksOverlappingActions(t *testing.T) {
	service := &fakeTUIService{
		tasks: []*core.Task{tuiTask("task-one"), tuiTask("task-two")},
	}
	m := newLoadedTUIModel(t, service, service.tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("x"))
	m, cleanupCmd := updateTUIModel(t, m, keyRunes("y"))
	require.NotNil(t, cleanupCmd)
	require.True(t, m.busy)

	selected := m.selected
	m, cmd := updateTUIModel(t, m, keyRunes("j"))
	require.Equal(t, selected, m.selected)
	require.Nil(t, cmd)

	m, cmd = updateTUIModel(t, m, keyRunes("r"))
	require.Nil(t, cmd)
	require.Equal(t, 0, service.listCalls)
}

func TestModelUpdate_RefreshInFlightBlocksOverlappingActions(t *testing.T) {
	service := &fakeTUIService{
		tasks: []*core.Task{tuiTask("task-one"), tuiTask("task-two")},
	}
	m := newLoadedTUIModel(t, service, service.tasks...)

	m, refreshCmd := updateTUIModel(t, m, keyRunes("r"))
	require.NotNil(t, refreshCmd)
	require.True(t, m.busy)

	selected := m.selected
	m, cmd := updateTUIModel(t, m, keyRunes("j"))
	require.Equal(t, selected, m.selected)
	require.Nil(t, cmd)

	m, cmd = updateTUIModel(t, m, keyRunes("x"))
	require.False(t, m.confirming)
	require.Nil(t, cmd)
}

func TestModelUpdate_MainListViewRendersTaskDetails(t *testing.T) {
	service := &fakeTUIService{}
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"
	task.Status = core.TaskStatusRunning
	task.SessionExists = true
	task.WorktreeExists = false
	task.BranchName = "feat/billing-retry-flow"

	m := newLoadedTUIModel(t, service, task)
	view := m.View()

	require.Contains(t, view, "billing retry flow")
	require.Contains(t, view, "running")
	require.Contains(t, view, "tmux: yes")
	require.Contains(t, view, "worktree: no")
	require.Contains(t, view, "feat/billing-retry-flow")
}

func TestModelUpdate_LoadedTasksHideTasksWithoutLiveResources(t *testing.T) {
	active := tuiTask("active-task")
	hidden := tuiTask("cleaned-task")
	hidden.Status = core.TaskStatusCleaned
	hidden.SessionExists = false
	hidden.WorktreeExists = false

	m := newLoadedTUIModel(t, &fakeTUIService{}, active, hidden)
	view := m.View()

	require.Contains(t, view, "active-task")
	require.NotContains(t, view, "cleaned-task")
}

func TestModelUpdate_ConfirmationViewExplainsDeletionScope(t *testing.T) {
	m := newLoadedTUIModel(t, &fakeTUIService{}, tuiTask("billing-retry-flow"))
	m, _ = updateTUIModel(t, m, keyRunes("x"))

	view := m.View()
	require.Contains(t, view, "tmux session and worktree will be deleted")
	require.Contains(t, view, "branch will be kept")
}

func TestModelUpdate_CleanupFailureRendersInlineErrorAndKeepsTUIUsable(t *testing.T) {
	service := &fakeTUIService{
		deleteErr: errors.New("cleanup failed"),
		tasks:     []*core.Task{tuiTask("task-one"), tuiTask("task-two")},
	}
	m := newLoadedTUIModel(t, service, service.tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("x"))

	_, cleanupCmd := updateTUIModel(t, m, keyRunes("y"))
	require.NotNil(t, cleanupCmd)

	msg := cleanupCmd()
	m, _ = updateTUIModel(t, m, msg)
	require.Contains(t, m.View(), "cleanup failed")

	m, _ = updateTUIModel(t, m, keyRunes("j"))
	require.Equal(t, 1, m.selected)
	require.False(t, m.confirming)
}

func TestModelUpdate_CleanupSuccessRefreshFailureRemovesTaskFromVisibleList(t *testing.T) {
	cleaned := tuiTask("task-one")
	cleaned.Status = core.TaskStatusCleaned
	cleaned.SessionExists = false
	cleaned.WorktreeExists = false

	service := &fakeTUIService{
		tasks:     []*core.Task{tuiTask("task-one")},
		deleteTask: cleaned,
		listErr:   errors.New("refresh failed"),
	}
	m := newLoadedTUIModel(t, service, service.tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("x"))

	m, cleanupCmd := updateTUIModel(t, m, keyRunes("y"))
	msg := cleanupCmd()
	m, refreshCmd := updateTUIModel(t, m, msg)
	require.NotNil(t, refreshCmd)

	refreshMsg := refreshCmd()
	m, _ = updateTUIModel(t, m, refreshMsg)

	view := m.View()
	require.NotContains(t, view, "task-one")
	require.Contains(t, view, "refresh failed")
	require.False(t, m.busy)
}

func TestModelView_ShowsLoadingBeforeInitialLoadCompletes(t *testing.T) {
	m := newTUIModel(&fakeTUIService{})
	require.Contains(t, m.View(), "Loading tasks")
}

type fakeTUIService struct {
	tasks           []*core.Task
	deleteTask      *core.Task
	deleteErr       error
	listErr         error
	deletedIDOrSlug string
	listCalls       int
}

func (*fakeTUIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return core.DoctorResult{}, nil
}

func (*fakeTUIService) SuggestTaskName(context.Context, string) (string, error) { return "", nil }

func (*fakeTUIService) NewTask(context.Context, core.NewTaskInput) (*core.Task, error) {
	return nil, nil
}

func (f *fakeTUIService) ListTasks(context.Context) ([]*core.Task, error) {
	f.listCalls++
	return f.tasks, f.listErr
}

func (*fakeTUIService) GetTask(context.Context, string) (*core.Task, error) { return nil, nil }

func (*fakeTUIService) OpenTask(context.Context, string) error { return nil }

func (f *fakeTUIService) DeleteTaskResources(_ context.Context, idOrSlug string) (*core.Task, error) {
	f.deletedIDOrSlug = idOrSlug
	if f.deleteTask != nil || f.deleteErr == nil {
		return f.deleteTask, f.deleteErr
	}

	return nil, f.deleteErr
}

func newLoadedTUIModel(t *testing.T, service TaskService, tasks ...*core.Task) model {
	t.Helper()

	next, cmd := newTUIModel(service).Update(tasksLoadedMsg{tasks: tasks})
	require.Nil(t, cmd)

	m, ok := next.(model)
	require.True(t, ok)
	return m
}

func updateTUIModel(t *testing.T, current model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()

	next, cmd := current.Update(msg)
	m, ok := next.(model)
	require.True(t, ok)
	return m, cmd
}

func keyRunes(chars string) tea.KeyMsg {
	return tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(chars),
	}
}

func tuiTask(slug string) *core.Task {
	return &core.Task{
		ID:             slug + "-id",
		Slug:           slug,
		DisplayName:    slug,
		Status:         core.TaskStatusRunning,
		BranchName:     "feat/" + slug,
		TmuxSession:    "repo-" + slug,
		SessionExists:  true,
		WorktreeExists: true,
	}
}
