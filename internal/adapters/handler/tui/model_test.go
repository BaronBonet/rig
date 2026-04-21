package tui

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestModel_InitLoadsAllTasksAcrossRepos(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoName:    "repo-a",
			DisplayName: "first task",
			Provider:    core.AgentProviderCodex,
		},
		{
			ID:          "task-2",
			RepoName:    "repo-b",
			DisplayName: "second task",
			Provider:    core.AgentProviderCodex,
		},
	}

	m := newModel(frontend)
	cmd := m.Init()
	require.NotNil(t, cmd)

	msg := runCmd(t, cmd)
	next, _ := m.Update(msg)

	got, ok := next.(model)
	require.True(t, ok)
	require.Len(t, got.rows, 2)
	require.Equal(t, []string{"task-1", "task-2"}, []string{got.rows[0].task.ID, got.rows[1].task.ID})
	require.Equal(t, 1, frontend.listTasksCalls)
}

func TestModel_ViewRendersTaskMetadata(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoName:    "repo-a",
			DisplayName: "first task",
			Provider:    core.AgentProviderCodex,
		},
	}

	m := newModel(frontend)
	msg := runCmd(t, m.Init())
	next, _ := m.Update(msg)

	got, ok := next.(model)
	require.True(t, ok)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "first task")
	require.Contains(t, view, "repo-a")
	require.Contains(t, view, "codex")
}

func TestModel_AfterLoadRequestsLatestStatusAndSubscriptionsForEachTask(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.AgentProviderCodex},
		{ID: "task-2", RepoName: "repo-b", DisplayName: "second task", Provider: core.AgentProviderCodex},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
		"task-2": make(chan core.TaskStatusUpdate, 1),
	}

	m := newModel(frontend)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	_, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	require.Len(t, msgs, 4)
	require.Equal(t, []string{"task-1", "task-2"}, frontend.latestTaskStatusCalls)
	require.Equal(t, []string{"task-1", "task-2"}, frontend.subscribeTaskStatusCalls)
}

func TestModel_LatestStatusSeedUpdatesRenderedPhase(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.AgentProviderCodex},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-1": {
			TaskID: "task-1",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
	}

	m := newModel(frontend)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	latestMsg := requireMsgType[latestTaskStatusLoadedMsg](t, msgs)

	next, _ = got.Update(latestMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, got.rows[0].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[0].status.Phase)
	require.Contains(t, stripANSI(got.View().Content), "phase: working")
}

func TestModel_TaskRowUpdatesWhenSubscriptionUpdateArrives(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.AgentProviderCodex},
	}
	updates := make(chan core.TaskStatusUpdate, 1)
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": updates,
	}

	m := newModel(frontend)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	subscribeMsg := requireMsgType[taskStatusSubscriptionReadyMsg](t, msgs)

	next, waitCmd := got.Update(subscribeMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, waitCmd)

	updates <- core.TaskStatusUpdate{
		TaskID: "task-1",
		Phase:  core.TaskStatusPhaseWaitingForInput,
	}

	updateMsg := runCmd(t, waitCmd)
	next, nextCmd := got.Update(updateMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, nextCmd)
	require.NotNil(t, got.rows[0].status)
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, got.rows[0].status.Phase)
	require.Contains(t, stripANSI(got.View().Content), "phase: waiting_for_input")
}

func TestModel_StatusEnrichmentFailuresDoNotCollapseListView(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.AgentProviderCodex},
		{ID: "task-2", RepoName: "repo-b", DisplayName: "second task", Provider: core.AgentProviderCodex},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-2": {
			TaskID: "task-2",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.latestTaskStatusErr = map[string]error{
		"task-1": errors.New("latest status unavailable"),
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
	}
	frontend.subscribeTaskStatusErr = map[string]error{
		"task-2": errors.New("subscription unavailable"),
	}

	m := newModel(frontend)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	for _, msg := range runBatchCmd(t, cmd) {
		next, _ = got.Update(msg)
		got, ok = next.(model)
		require.True(t, ok)
	}

	require.NoError(t, got.err)
	require.Len(t, got.rows, 2)
	require.Nil(t, got.rows[0].status)
	require.NotNil(t, got.rows[1].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[1].status.Phase)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "first task")
	require.Contains(t, view, "second task")
	require.Contains(t, view, "phase: working")
	require.NotContains(t, view, "latest status unavailable")
	require.NotContains(t, view, "subscription unavailable")
}

func TestModel_InitUsesLifecycleContextForInitialLoad(t *testing.T) {
	frontend := newStubFrontend()
	m := newModel(frontend)

	cmd := m.Init()
	require.NotNil(t, cmd)

	m.cancelStatus()
	runCmd(t, cmd)

	require.NotNil(t, frontend.listTasksContext)
	require.ErrorIs(t, frontend.listTasksContext.Err(), context.Canceled)
}

func TestModel_KeyAEntersPromptMode(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "a"})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Empty(t, got.prompt)
}

func TestModel_CreateTaskFromPromptAppendsTaskAndStartsStatusTracking(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.AgentProviderCodex},
		{ID: "task-2", DisplayName: "second task", RepoName: "repo-b", Provider: core.AgentProviderCodex},
	}
	frontend.createdTask = &core.Task{
		ID:          "task-3",
		DisplayName: "new task",
		RepoName:    "repo-c",
		Provider:    core.AgentProviderCodex,
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-3": {
			TaskID: "task-3",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
		"task-2": make(chan core.TaskStatusUpdate, 1),
		"task-3": make(chan core.TaskStatusUpdate, 1),
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	submitted, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, submitted.mode)
	require.Equal(t, "fix the retry loop", submitted.prompt)
	require.True(t, submitted.createPending)

	msg := runCmd(t, cmd)
	next, follow := submitted.Update(msg)
	require.NotNil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Len(t, got.rows, 3)
	require.Equal(t, modeBrowse, got.mode)
	require.Empty(t, got.prompt)
	require.False(t, got.createPending)
	require.Equal(t, "task-3", got.rows[len(got.rows)-1].task.ID)
	require.Equal(t, "fix the retry loop", frontend.createInput.Prompt)
	require.Equal(t, core.AgentProviderCodex, frontend.createInput.Provider)
	require.Equal(t, 1, frontend.createTaskCalls)

	msgs := runBatchCmd(t, follow)
	require.Len(t, msgs, 2)
	require.Equal(t, []string{"task-3"}, frontend.latestTaskStatusCalls)
	require.Equal(t, []string{"task-3"}, frontend.subscribeTaskStatusCalls)

	latestMsg := requireMsgType[latestTaskStatusLoadedMsg](t, msgs)
	next, _ = got.Update(latestMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, got.rows[2].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[2].status.Phase)
}

func TestModel_EnterWithBlankPromptDoesNothing(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "   "

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "   ", got.prompt)
	require.False(t, got.createPending)
	require.Zero(t, frontend.createTaskCalls)
}

func TestModel_CreateTaskFailureKeepsPromptRecoverableAndPreservesListView(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.AgentProviderCodex},
		{ID: "task-2", DisplayName: "second task", RepoName: "repo-b", Provider: core.AgentProviderCodex},
	}
	frontend.createTaskErr = errors.New("create failed")

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.True(t, pending.createPending)

	msg := runCmd(t, cmd)
	next, follow := pending.Update(msg)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "fix the retry loop", got.prompt)
	require.False(t, got.createPending)
	require.ErrorContains(t, got.createErr, "create failed")
	require.NoError(t, got.err)
	require.Len(t, got.rows, 2)
	require.Zero(t, len(frontend.latestTaskStatusCalls))
	require.Zero(t, len(frontend.subscribeTaskStatusCalls))

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "first task")
	require.Contains(t, view, "second task")
	require.Contains(t, view, "New task prompt")
	require.Contains(t, view, "fix the retry loop")
	require.Contains(t, view, "create failed")
	require.NotContains(t, view, "Loading tasks...")
}

func TestModel_PendingCreateStillAllowsQuitKeys(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"
	m.createPending = true

	for _, msg := range []tea.KeyPressMsg{
		{Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		next, cmd := m.Update(msg)
		require.NotNil(t, cmd)

		got, ok := next.(model)
		require.True(t, ok)
		require.True(t, got.createPending)

		quitMsg := runCmd(t, cmd)
		_, ok = quitMsg.(tea.QuitMsg)
		require.True(t, ok)
	}
}

func TestModel_EscCancelsPromptMode(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.Empty(t, got.prompt)
	require.False(t, got.createPending)
	require.NoError(t, got.createErr)
	require.Zero(t, frontend.createTaskCalls)
}

func newLoadedModel(frontend *stubFrontend) model {
	return model{
		frontend:      frontend,
		statusContext: context.Background(),
		rows:          rowsFromTasks(frontend.listTasks),
		mode:          modeBrowse,
	}
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)
	return cmd()
}

func runBatchCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	msg := runCmd(t, cmd)
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok)

	msgs := make([]tea.Msg, 0, len(batch))
	for _, batchCmd := range batch {
		msgs = append(msgs, runCmd(t, batchCmd))
	}
	return msgs
}

func requireMsgType[T tea.Msg](t *testing.T, msgs []tea.Msg) T {
	t.Helper()

	for _, msg := range msgs {
		typed, ok := msg.(T)
		if ok {
			return typed
		}
	}

	var zero T
	t.Fatalf("message of type %T not found", zero)
	return zero
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

type stubFrontend struct {
	listTasks                []*core.Task
	listTasksContext         context.Context
	listTasksErr             error
	listTasksCalls           int
	createdTask              *core.Task
	createInput              core.CreateTaskInput
	createTaskErr            error
	createTaskCalls          int
	latestTaskStatus         map[string]*core.TaskStatusUpdate
	latestTaskStatusErr      map[string]error
	latestTaskStatusCalls    []string
	subscribeTaskStatus      map[string]chan core.TaskStatusUpdate
	subscribeTaskStatusErr   map[string]error
	subscribeTaskStatusCalls []string
}

func newStubFrontend() *stubFrontend {
	return &stubFrontend{}
}

func (s *stubFrontend) CreateTask(_ context.Context, input core.CreateTaskInput) (*core.Task, error) {
	s.createTaskCalls++
	s.createInput = input
	return s.createdTask, s.createTaskErr
}

func (s *stubFrontend) ListTasks(ctx context.Context) ([]*core.Task, error) {
	s.listTasksCalls++
	s.listTasksContext = ctx
	return s.listTasks, s.listTasksErr
}

func (s *stubFrontend) LatestTaskStatus(_ context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	s.latestTaskStatusCalls = append(s.latestTaskStatusCalls, taskID)
	if s.latestTaskStatusErr != nil && s.latestTaskStatusErr[taskID] != nil {
		return nil, s.latestTaskStatusErr[taskID]
	}
	if s.latestTaskStatus == nil {
		return nil, nil
	}
	return s.latestTaskStatus[taskID], nil
}

func (s *stubFrontend) SubscribeTaskStatus(_ context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	s.subscribeTaskStatusCalls = append(s.subscribeTaskStatusCalls, taskID)
	if s.subscribeTaskStatusErr != nil && s.subscribeTaskStatusErr[taskID] != nil {
		return nil, s.subscribeTaskStatusErr[taskID]
	}
	if s.subscribeTaskStatus == nil {
		ch := make(chan core.TaskStatusUpdate)
		close(ch)
		return ch, nil
	}
	return s.subscribeTaskStatus[taskID], nil
}
