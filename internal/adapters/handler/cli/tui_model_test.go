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
	require.Equal(t, tuiModeCleanupConfirm, m.mode)
	require.Nil(t, cmd)
}

func TestModelUpdate_NEntersPromptEntryMode(t *testing.T) {
	m := newLoadedTUIModel(t, &fakeTUIService{}, tuiTask("task-one"))

	m, cmd := updateTUIModel(t, m, keyRunes("n"))
	require.Equal(t, tuiModePromptInput, m.mode)
	require.True(t, m.promptInput.Focused())
	require.Nil(t, cmd)
}

func TestModelUpdate_CreateFlowSuggestsNameThenCreatesTask(t *testing.T) {
	service := &fakeTUIService{
		suggestedName: "billing retry flow",
		createdTask:   tuiTask("billing-retry-flow"),
	}
	m := newLoadedTUIModel(t, service, tuiTask("existing-task"))

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")

	m, suggestCmd := updateTUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, suggestCmd)

	suggestMsg := suggestCmd()
	require.Equal(t, "add billing retry flow", service.suggestedPrompt)
	m, _ = updateTUIModel(t, m, suggestMsg)
	require.Equal(t, tuiModeNameConfirm, m.mode)
	require.Equal(t, "billing retry flow", m.nameInput.Value())

	m, createCmd := updateTUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, createCmd)
	require.True(t, m.busy)

	createMsg := createCmd()
	m, quitCmd := updateTUIModel(t, m, createMsg)
	require.Equal(t, "add billing retry flow", service.createdInput.Prompt)
	require.Equal(t, "billing retry flow", service.createdInput.ConfirmedDisplayName)
	require.True(t, service.createOptions.OpenSession)
	require.NotNil(t, quitCmd)

	quitMsg := quitCmd()
	_, ok := quitMsg.(tea.QuitMsg)
	require.True(t, ok)
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
			require.Equal(t, tuiModeList, m.mode)
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
	require.Equal(t, tuiModeList, m.mode)
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

func TestModelUpdate_EnterDispatchesOpenAndQuitsOnSuccess(t *testing.T) {
	service := &fakeTUIService{
		tasks: []*core.Task{tuiTask("task-one"), tuiTask("task-two")},
	}
	m := newLoadedTUIModel(t, service, service.tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("j"))

	m, cmd := updateTUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.True(t, m.busy)

	msg := cmd()
	m, cmd = updateTUIModel(t, m, msg)
	require.Equal(t, "task-two", service.openedIDOrSlug)
	require.NotNil(t, cmd)

	quitMsg := cmd()
	_, ok := quitMsg.(tea.QuitMsg)
	require.True(t, ok)
}

func TestModelUpdate_EnterFailureRendersInlineErrorAndKeepsTUIOpen(t *testing.T) {
	service := &fakeTUIService{
		tasks:   []*core.Task{tuiTask("task-one"), tuiTask("task-two")},
		openErr: errors.New("open failed"),
	}
	m := newLoadedTUIModel(t, service, service.tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("j"))

	m, cmd := updateTUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.True(t, m.busy)

	msg := cmd()
	m, followup := updateTUIModel(t, m, msg)
	require.Equal(t, "task-two", service.openedIDOrSlug)
	require.Nil(t, followup)
	require.False(t, m.busy)
	require.Contains(t, m.View(), "open failed")

	m, _ = updateTUIModel(t, m, keyRunes("k"))
	require.Equal(t, 0, m.selected)
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
	require.Equal(t, tuiModeList, m.mode)
	require.Nil(t, cmd)
}

func TestModelUpdate_MainListViewRendersControlCenterDetails(t *testing.T) {
	service := &fakeTUIService{}
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"
	task.Status = core.TaskStatusRunning
	task.RepoName = "tmux-llm"
	task.SessionExists = true
	task.AgentWindowExists = true
	task.EditorWindowExists = false
	task.WorktreeExists = false
	task.BranchName = "feat/billing-retry-flow"

	m := newLoadedTUIModel(t, service, task)
	view := m.View()

	require.Contains(t, view, "Control Center")
	require.Contains(t, view, "repo: tmux-llm")
	require.Contains(t, view, "agent: healthy")
	require.Contains(t, view, "editor: missing")
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
	require.Equal(t, tuiModeList, m.mode)
}

func TestModelUpdate_CleanupSuccessRefreshFailureRemovesTaskFromVisibleList(t *testing.T) {
	cleaned := tuiTask("task-one")
	cleaned.Status = core.TaskStatusCleaned
	cleaned.SessionExists = false
	cleaned.WorktreeExists = false

	service := &fakeTUIService{
		tasks:      []*core.Task{tuiTask("task-one")},
		deleteTask: cleaned,
		listErr:    errors.New("refresh failed"),
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
	suggestedName   string
	suggestErr      error
	suggestedPrompt string
	createdTask     *core.Task
	createdInput    core.NewTaskInput
	createOptions   core.CreateTaskOptions
	createErr       error
	deletedIDOrSlug string
	openedIDOrSlug  string
	openErr         error
	listCalls       int
}

func (*fakeTUIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return core.DoctorResult{}, nil
}

func (f *fakeTUIService) SuggestTaskName(_ context.Context, prompt string) (string, error) {
	f.suggestedPrompt = prompt
	return f.suggestedName, f.suggestErr
}

func (*fakeTUIService) NewTask(context.Context, core.NewTaskInput) (*core.Task, error) {
	return nil, nil
}

func (f *fakeTUIService) CreateTaskWithProgress(
	_ context.Context,
	input core.NewTaskInput,
	options core.CreateTaskOptions,
	_ func(core.TaskProgress),
) (*core.Task, error) {
	f.createdInput = input
	f.createOptions = options
	return f.createdTask, f.createErr
}

func (f *fakeTUIService) ListTasks(context.Context) ([]*core.Task, error) {
	f.listCalls++
	return f.tasks, f.listErr
}

func (*fakeTUIService) GetTask(context.Context, string) (*core.Task, error) { return nil, nil }

func (f *fakeTUIService) OpenTask(_ context.Context, idOrSlug string) error {
	f.openedIDOrSlug = idOrSlug
	return f.openErr
}

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
