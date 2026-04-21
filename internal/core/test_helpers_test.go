package core

import (
	"context"
	"sync"
	"testing"
)

type testTaskServiceHarness struct {
	service TaskService

	taskRepoMock *stubTaskRepository
	taskRepo     taskRepositoryState

	repoClientMock *stubGitWorktreeClient
	repoClient     repoClientState

	sessionClientMock *stubTmuxSessionClient
	sessionClient     sessionClientState

	providerRepo providerClientState
	workspace    workspaceManagerState
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
	hookErr             error
	hookUpdate          *TaskStatusUpdate
	hookInput           HookEventInput
}

type workspaceManagerState struct {
	setupErr                     error
	bootstrapErr                 error
	setupCalled                  bool
	bootstrapCalled              bool
	repoRoot                     string
	worktreePath                 string
	bootstrapSpec                WorkspaceBootstrapSpec
	setupCalledBeforeSession     bool
	bootstrapCalledBeforeSession bool
	preparedDisplayName          string
	preparedBranchName           string
}

type recordingWorkspaceManager struct {
	state   *workspaceManagerState
	session *sessionClientState
}

type recordingAgentClient struct {
	state *providerClientState
}

type stubGitWorktreeClient struct {
	state *repoClientState
}

type stubTmuxSessionClient struct {
	state *sessionClientState
}

type stubTaskRepository struct {
	state *taskRepositoryState
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

func (c *recordingAgentClient) HookEventToTaskStatus(input HookEventInput) (*TaskStatusUpdate, error) {
	c.state.hookInput = input
	if c.state.hookErr != nil {
		return nil, c.state.hookErr
	}
	if c.state.hookUpdate == nil {
		return nil, nil
	}

	update := *c.state.hookUpdate
	return &update, nil
}

func (p *recordingWorkspaceManager) SetupTaskWorkspace(
	_ context.Context,
	task *Task,
	repoRoot string,
) error {
	p.state.setupCalled = true
	p.state.repoRoot = repoRoot
	if task != nil {
		p.state.worktreePath = task.WorktreePath
		p.state.preparedDisplayName = task.DisplayName
		p.state.preparedBranchName = task.BranchName
	}
	p.state.setupCalledBeforeSession = p.session.startedTask == nil
	return p.state.setupErr
}

func (p *recordingWorkspaceManager) BootstrapTaskWorkspace(
	_ context.Context,
	task *Task,
	bootstrapSpec WorkspaceBootstrapSpec,
) error {
	p.state.bootstrapCalled = true
	p.state.bootstrapSpec = bootstrapSpec
	if task != nil {
		p.state.worktreePath = task.WorktreePath
		p.state.preparedDisplayName = task.DisplayName
		p.state.preparedBranchName = task.BranchName
	}
	p.state.bootstrapCalledBeforeSession = p.session.startedTask == nil
	return p.state.bootstrapErr
}

func newTestTaskService(t *testing.T) *testTaskServiceHarness {
	t.Helper()

	h := &testTaskServiceHarness{
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
	h.taskRepoMock = &stubTaskRepository{state: &h.taskRepo}
	h.repoClientMock = &stubGitWorktreeClient{state: &h.repoClient}
	h.sessionClientMock = &stubTmuxSessionClient{state: &h.sessionClient}

	h.service = NewTaskService(TaskServiceDependencies{
		Tasks:       h.taskRepoMock,
		GitWorktree: h.repoClientMock,
		TmuxSession: h.sessionClientMock,
		Agents: map[AgentProvider]AgentClient{
			AgentProviderCodex: &recordingAgentClient{state: &h.providerRepo},
		},
		Workspace:            &recordingWorkspaceManager{state: &h.workspace, session: &h.sessionClient},
		EnableWorkspaceSetup: true,
		DefaultProvider:      AgentProviderCodex,
	})

	return h
}

func (s *stubGitWorktreeClient) DetectRepo(context.Context, string) (RepoContext, error) {
	if s.state.detectRepoErr != nil {
		return RepoContext{}, s.state.detectRepoErr
	}

	return s.state.repoContext, nil
}

func (s *stubGitWorktreeClient) IsBranchUsedByWorktree(_ context.Context, _ string, branchName string) (bool, error) {
	if s.state.branchInUseErr != nil {
		return false, s.state.branchInUseErr
	}

	return s.state.branchInUse[branchName], nil
}

func (s *stubGitWorktreeClient) CreateTaskWorkspace(_ context.Context, task *Task) error {
	s.state.createdTask = cloneTask(task)
	if s.state.createErr != nil {
		return s.state.createErr
	}

	s.state.repoResources.WorktreeExists = true
	s.state.repoResources.BranchExists = true
	return nil
}

func (s *stubGitWorktreeClient) CreateTaskWorkspaceFromBranch(_ context.Context, task *Task) error {
	s.state.createdTask = cloneTask(task)
	if s.state.createErr != nil {
		return s.state.createErr
	}

	s.state.repoResources.WorktreeExists = true
	s.state.repoResources.BranchExists = true
	return nil
}

func (s *stubTmuxSessionClient) StartTaskSession(_ context.Context, task *Task, launch TaskSessionLaunchSpec) error {
	s.state.startedTask = cloneTask(task)
	s.state.startedLaunch = launch
	if s.state.startErr != nil {
		return s.state.startErr
	}

	s.state.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	return nil
}

func (s *stubTmuxSessionClient) OpenTaskSession(context.Context, *Task) error {
	return nil
}

func (s *stubTmuxSessionClient) DeleteTaskSession(context.Context, *Task) error {
	return nil
}

func (s *stubTmuxSessionClient) InspectTaskSession(context.Context, *Task) (SessionResources, error) {
	return s.state.sessionResources, nil
}

func (s *stubTmuxSessionClient) SnapshotTaskSession(context.Context, *Task) (RuntimeSnapshot, error) {
	return RuntimeSnapshot{}, nil
}

func (s *stubTaskRepository) CreateTask(_ context.Context, task *Task) error {
	if s.state.createErr != nil {
		return s.state.createErr
	}

	s.state.createdTask = cloneTask(task)
	return nil
}

func (s *stubTaskRepository) UpdateTask(_ context.Context, task *Task) error {
	s.state.updateCount++
	if s.state.updateErr != nil &&
		(s.state.updateErrAt == 0 || s.state.updateCount == s.state.updateErrAt) {
		return s.state.updateErr
	}

	s.state.updatedTask = cloneTask(task)
	return nil
}

func (s *stubTaskRepository) ListTasks(context.Context) ([]*Task, error) {
	tasks := make([]*Task, 0, len(s.state.listTasks))
	for _, task := range s.state.listTasks {
		tasks = append(tasks, cloneTask(task))
	}

	return tasks, nil
}

func (s *stubTaskRepository) UpsertTaskStatus(_ context.Context, update TaskStatusUpdate) error {
	s.state.mu.Lock()
	s.state.latestByTask[update.TaskID] = update
	subscribers := append([]chan TaskStatusUpdate(nil), s.state.subscribers[update.TaskID]...)
	s.state.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber <- update
	}
	return nil
}

func (s *stubTaskRepository) LatestTaskStatus(_ context.Context, taskID string) (*TaskStatusUpdate, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	update, ok := s.state.latestByTask[taskID]
	if !ok {
		return nil, nil
	}
	copy := update
	return &copy, nil
}

func (s *stubTaskRepository) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error) {
	updates := make(chan TaskStatusUpdate, 8)

	s.state.mu.Lock()
	s.state.subscribers[taskID] = append(s.state.subscribers[taskID], updates)
	s.state.mu.Unlock()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			s.state.mu.Lock()
			defer s.state.mu.Unlock()

			subscribers := s.state.subscribers[taskID]
			filtered := subscribers[:0]
			for _, subscriber := range subscribers {
				if subscriber != updates {
					filtered = append(filtered, subscriber)
				}
			}
			if len(filtered) == 0 {
				delete(s.state.subscribers, taskID)
			} else {
				s.state.subscribers[taskID] = filtered
			}
			close(updates)
		})
	}

	go func() {
		<-ctx.Done()
		cleanup()
	}()

	return updates, nil
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
