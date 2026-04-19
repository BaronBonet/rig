package core

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/mock"
)

type testTaskServiceHarness struct {
	service TaskService

	taskRepoMock *MockTaskRepository
	taskRepo     taskRepositoryState

	repoClientMock *MockRepoClient
	repoClient     repoClientState

	sessionClientMock *MockTmuxSessionClient
	sessionClient     sessionClientState

	providerRepo providerClientState
	preparer     workspacePreparerState
}

type taskRepositoryState struct {
	createErr    error
	updateErr    error
	updateErrAt  int
	updateCount  int
	listTasks    []*Task
	createdTask  *Task
	updatedTask  *Task
	mu           sync.Mutex
	latestByTask map[string]TaskStatusUpdate
	subscribers  map[string][]chan TaskStatusUpdate
}

type repoClientState struct {
	detectRepoErr  error
	branchInUseErr error
	createErr      error
	repoContext    RepoContext
	repoResources  RepoResources
	branchInUse    map[string]bool
	createdTask    *Task
}

type sessionClientState struct {
	startErr         error
	startedTask      *Task
	startedLaunch    TaskSessionLaunchSpec
	sessionResources SessionResources
}

type providerClientState struct {
	suggestErr          error
	suggestedName       string
	suggestedSuggestion TaskSuggestion
	bootstrapErr        error
	bootstrapSpec       WorkspaceBootstrapSpec
	bootstrapRequest    *Task
	launchErr           error
	launchRequest       TaskSessionLaunchSpec
}

type workspacePreparerState struct {
	prepareErr          error
	called              bool
	repoRoot            string
	worktreePath        string
	bootstrapSpec       WorkspaceBootstrapSpec
	calledBeforeSession bool
	preparedDisplayName string
	preparedBranchName  string
}

type recordingWorkspacePreparer struct {
	state   *workspacePreparerState
	session *sessionClientState
}

type recordingAgentClient struct {
	state *providerClientState
}

func (c *recordingAgentClient) SuggestTaskName(_ context.Context, _ string) (TaskSuggestion, error) {
	if c.state.suggestErr != nil {
		return TaskSuggestion{}, c.state.suggestErr
	}
	if c.state.suggestedSuggestion.Name != "" {
		return c.state.suggestedSuggestion, nil
	}
	return TaskSuggestion{Name: c.state.suggestedName, BranchType: "feat"}, nil
}

func (c *recordingAgentClient) BuildWorkspaceBootstrapSpec(task *Task) (WorkspaceBootstrapSpec, error) {
	c.state.bootstrapRequest = cloneTask(task)
	if c.state.bootstrapErr != nil {
		return WorkspaceBootstrapSpec{}, c.state.bootstrapErr
	}
	return c.state.bootstrapSpec, nil
}

func (c *recordingAgentClient) BuildTaskSessionLaunchSpec(task *Task) (TaskSessionLaunchSpec, error) {
	if c.state.launchErr != nil {
		return TaskSessionLaunchSpec{}, c.state.launchErr
	}
	if hasCustomLaunchSpec(c.state.launchRequest) {
		return c.state.launchRequest, nil
	}

	return TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{task.Prompt},
	}, nil
}

func (p *recordingWorkspacePreparer) PrepareTaskWorkspace(
	_ context.Context,
	task *Task,
	repoRoot string,
	bootstrapSpec WorkspaceBootstrapSpec,
) error {
	p.state.called = true
	p.state.repoRoot = repoRoot
	p.state.bootstrapSpec = bootstrapSpec
	if task != nil {
		p.state.worktreePath = task.WorktreePath
		p.state.preparedDisplayName = task.DisplayName
		p.state.preparedBranchName = task.BranchName
	}
	p.state.calledBeforeSession = p.session.startedTask == nil
	return p.state.prepareErr
}

func newTestTaskService(t *testing.T) *testTaskServiceHarness {
	t.Helper()

	h := &testTaskServiceHarness{
		taskRepoMock:      NewMockTaskRepository(t),
		repoClientMock:    NewMockRepoClient(t),
		sessionClientMock: NewMockTmuxSessionClient(t),
		repoClient: repoClientState{
			repoContext: RepoContext{
				Root:       "/tmp/repo",
				Name:       "repo",
				BaseBranch: "main",
			},
			branchInUse: map[string]bool{},
		},
	}
	h.taskRepo.latestByTask = make(map[string]TaskStatusUpdate)
	h.taskRepo.subscribers = make(map[string][]chan TaskStatusUpdate)

	wireTaskRepositoryMock(h)
	wireRepoClientMock(h)
	wireSessionClientMock(h)

	h.service = NewTaskService(TaskServiceDependencies{
		Tasks:           h.taskRepoMock,
		GitWorktree:     h.repoClientMock,
		TmuxSession:     h.sessionClientMock,
		Agents:          map[string]AgentClient{"codex": &recordingAgentClient{state: &h.providerRepo}},
		Preparer:        &recordingWorkspacePreparer{state: &h.preparer, session: &h.sessionClient},
		DefaultProvider: AgentProviderCodex,
	})

	return h
}

func wireTaskRepositoryMock(h *testTaskServiceHarness) {
	h.taskRepoMock.EXPECT().CreateTask(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			if h.taskRepo.createErr != nil {
				return h.taskRepo.createErr
			}

			h.taskRepo.createdTask = cloneTask(task)
			return nil
		}).Maybe()
	h.taskRepoMock.EXPECT().UpdateTask(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.taskRepo.updateCount++
			if h.taskRepo.updateErr != nil &&
				(h.taskRepo.updateErrAt == 0 || h.taskRepo.updateCount == h.taskRepo.updateErrAt) {
				return h.taskRepo.updateErr
			}

			h.taskRepo.updatedTask = cloneTask(task)
			return nil
		}).Maybe()
	h.taskRepoMock.EXPECT().ListTasks(mock.Anything).RunAndReturn(func(context.Context) ([]*Task, error) {
		tasks := make([]*Task, 0, len(h.taskRepo.listTasks))
		for _, task := range h.taskRepo.listTasks {
			tasks = append(tasks, cloneTask(task))
		}

		return tasks, nil
	}).Maybe()
	h.taskRepoMock.EXPECT().UpsertTaskStatus(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, update TaskStatusUpdate) error {
			h.taskRepo.mu.Lock()
			h.taskRepo.latestByTask[update.TaskID] = update
			subscribers := append([]chan TaskStatusUpdate(nil), h.taskRepo.subscribers[update.TaskID]...)
			h.taskRepo.mu.Unlock()

			for _, subscriber := range subscribers {
				subscriber <- update
			}
			return nil
		}).Maybe()
	h.taskRepoMock.EXPECT().LatestTaskStatus(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, taskID string) (*TaskStatusUpdate, error) {
			h.taskRepo.mu.Lock()
			defer h.taskRepo.mu.Unlock()

			update, ok := h.taskRepo.latestByTask[taskID]
			if !ok {
				return nil, nil
			}
			copy := update
			return &copy, nil
		}).Maybe()
	h.taskRepoMock.EXPECT().SubscribeTaskStatus(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error) {
			updates := make(chan TaskStatusUpdate, 8)

			h.taskRepo.mu.Lock()
			h.taskRepo.subscribers[taskID] = append(h.taskRepo.subscribers[taskID], updates)
			h.taskRepo.mu.Unlock()

			var once sync.Once
			cleanup := func() {
				once.Do(func() {
					h.taskRepo.mu.Lock()
					defer h.taskRepo.mu.Unlock()

					subscribers := h.taskRepo.subscribers[taskID]
					filtered := subscribers[:0]
					for _, subscriber := range subscribers {
						if subscriber != updates {
							filtered = append(filtered, subscriber)
						}
					}
					if len(filtered) == 0 {
						delete(h.taskRepo.subscribers, taskID)
					} else {
						h.taskRepo.subscribers[taskID] = filtered
					}
					close(updates)
				})
			}

			go func() {
				<-ctx.Done()
				cleanup()
			}()

			return updates, nil
		}).Maybe()
}

func wireRepoClientMock(h *testTaskServiceHarness) {
	h.repoClientMock.EXPECT().DetectRepo(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, string) (RepoContext, error) {
			if h.repoClient.detectRepoErr != nil {
				return RepoContext{}, h.repoClient.detectRepoErr
			}

			return h.repoClient.repoContext, nil
		}).Maybe()
	h.repoClientMock.EXPECT().IsBranchUsedByWorktree(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, branchName string) (bool, error) {
			if h.repoClient.branchInUseErr != nil {
				return false, h.repoClient.branchInUseErr
			}

			return h.repoClient.branchInUse[branchName], nil
		}).Maybe()
	h.repoClientMock.EXPECT().CreateTaskWorkspace(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.repoClient.createdTask = cloneTask(task)
			if h.repoClient.createErr != nil {
				return h.repoClient.createErr
			}

			h.repoClient.repoResources.WorktreeExists = true
			h.repoClient.repoResources.BranchExists = true
			return nil
		}).Maybe()
	h.repoClientMock.EXPECT().CreateTaskWorkspaceFromBranch(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.repoClient.createdTask = cloneTask(task)
			if h.repoClient.createErr != nil {
				return h.repoClient.createErr
			}

			h.repoClient.repoResources.WorktreeExists = true
			h.repoClient.repoResources.BranchExists = true
			return nil
		}).Maybe()
}

func wireSessionClientMock(h *testTaskServiceHarness) {
	h.sessionClientMock.EXPECT().StartTaskSession(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task, launch TaskSessionLaunchSpec) error {
			h.sessionClient.startedTask = cloneTask(task)
			h.sessionClient.startedLaunch = launch
			if h.sessionClient.startErr != nil {
				return h.sessionClient.startErr
			}

			h.sessionClient.sessionResources = SessionResources{
				SessionExists:      true,
				AgentWindowExists:  true,
				EditorWindowExists: true,
			}
			return nil
		}).Maybe()
}

func hasCustomLaunchSpec(req TaskSessionLaunchSpec) bool {
	return len(req.Command) > 0 || len(req.PrefillInput) > 0 || req.ReadyMarker != ""
}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}

	copy := *task
	return &copy
}
