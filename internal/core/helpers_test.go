package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
)

type testTaskServiceHarness struct {
	service TaskService

	taskRepoMock *MockTaskRepository
	taskRepo     taskRepositoryState

	repoClientMock *MockGitWorktreeClient
	repoClient     repoClientState

	sessionClientMock *MockTmuxSessionClient
	sessionClient     sessionClientState

	pullRequestClientMock *MockPullRequestClient
	pullRequests          pullRequestClientState
	providerClientMock    *MockProviderClient
	providerRepo          providerClientState
	workspaceMock         *MockTaskWorkspaceManager
	workspace             workspaceManagerState
}

type taskRepositoryState struct {
	healthErr              error
	createErr              error
	updateErr              error
	deleteErr              error
	resumeMetadataErr      error
	updateErrAt            int
	updateCount            int
	listTasks              []*Task
	createdTask            *Task
	updatedTask            *Task
	deletedTaskID          string
	savedResumeMetadata    *TaskResumeMetadata
	savedProviderSessions  []TaskProviderSession
	latestResumeByTask     map[string]TaskResumeMetadata
	providerSessionsByTask map[string][]TaskProviderSession
	activityByTask         map[string][]TaskActivityEvent
	mu                     sync.Mutex
	latestByTask           map[string]TaskStatusUpdate
	subscribers            map[string][]chan TaskStatusUpdate
}

type repoClientState struct {
	healthErr       error
	detectRepoErr   error
	branchInUseErr  error
	createErr       error
	removeErr       error
	repoContext     RepoContext
	branchInUse     map[string]bool
	createdTask     *Task
	removedTask     *Task
	createdPRNumber int
}

type sessionClientState struct {
	healthErr     error
	startErr      error
	deleteErr     error
	inspectErr    error
	startedTask   *Task
	deletedTask   *Task
	startedLaunch TaskSessionLaunchSpec
	inspectState  TaskSessionRuntimeState
}

type providerClientState struct {
	healthErr               error
	suggestErr              error
	suggestedName           string
	suggestedSuggestion     TaskSuggestion
	sessionEnvErr           error
	sessionEnvCalls         int
	bootstrapErr            error
	bootstrapSpec           WorkspaceBootstrapSpec
	bootstrapRequest        *Task
	launchErr               error
	launchRequest           TaskSessionLaunchSpec
	reconnectLaunchErr      error
	reconnectLaunch         TaskSessionLaunchSpec
	hookErr                 error
	hookUpdate              *TaskStatusUpdate
	hookInput               HookEventInput
	statusRecoveryErr       error
	statusRecoveryUpdate    *TaskStatusUpdate
	statusRecoveryCurrent   *TaskStatusUpdate
	statusRecoverySessions  []TaskProviderSession
	activityErrByTranscript map[string]error
	activityByTranscript    map[string][]TaskActivityEvent
	activityCalls           []providerActivityCall
	errByTranscript         map[string]error
	usageByTranscript       map[string]*SessionTokenUsage
	tokenUsageCalls         []providerTokenUsageCall
}

type pullRequestClientState struct {
	healthErr            error
	listErr              error
	checkStatusErr       error
	listRepoPullRequests []RepoPullRequest
	statusByBranch       map[string]*PRStatus
	statusSequence       []*PRStatus
	lastListRepoRoot     string
	lastStatusRepoRoot   string
	checkStatusCalls     int
}

type providerTokenUsageCall struct {
	transcriptPath string
}

type providerActivityCall struct {
	after          time.Time
	transcriptPath string
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
	h.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"codex"},
	}
	h.taskRepo.latestByTask = make(map[string]TaskStatusUpdate)
	h.taskRepo.latestResumeByTask = make(map[string]TaskResumeMetadata)
	h.taskRepo.providerSessionsByTask = make(map[string][]TaskProviderSession)
	h.taskRepo.activityByTask = make(map[string][]TaskActivityEvent)
	h.taskRepo.subscribers = make(map[string][]chan TaskStatusUpdate)
	h.taskRepoMock = NewMockTaskRepository(t)
	h.repoClientMock = NewMockGitWorktreeClient(t)
	h.sessionClientMock = NewMockTmuxSessionClient(t)
	h.pullRequestClientMock = NewMockPullRequestClient(t)
	h.providerClientMock = NewMockProviderClient(t)
	h.workspaceMock = NewMockTaskWorkspaceManager(t)
	configureTaskRepositoryMock(h.taskRepoMock, &h.taskRepo)
	configureGitWorktreeMock(h.repoClientMock, &h.repoClient)
	configureTmuxSessionMock(h.sessionClientMock, &h.sessionClient)
	configurePullRequestClientMock(h.pullRequestClientMock, &h.pullRequests)
	configureProviderClientMock(h.providerClientMock, &h.providerRepo)
	configureWorkspaceManagerMock(h.workspaceMock, &h.workspace, &h.sessionClient)

	h.service = NewTaskService(TaskServiceDependencies{
		Tasks:        h.taskRepoMock,
		GitWorktree:  h.repoClientMock,
		TmuxSession:  h.sessionClientMock,
		PullRequests: h.pullRequestClientMock,
		Providers: map[Provider]ProviderClient{
			ProviderCodex: h.providerClientMock,
		},
		Workspace:            h.workspaceMock,
		EnableWorkspaceSetup: true,
		DefaultProvider:      ProviderCodex,
	})

	return h
}

func configureGitWorktreeMock(client *MockGitWorktreeClient, state *repoClientState) {
	client.EXPECT().HealthCheck(mock.Anything).RunAndReturn(
		func(context.Context) error {
			return state.healthErr
		},
	).Maybe()
	client.EXPECT().DetectRepo(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, string) (RepoContext, error) {
			if state.detectRepoErr != nil {
				return RepoContext{}, state.detectRepoErr
			}
			return state.repoContext, nil
		},
	).Maybe()
	client.EXPECT().IsBranchUsedByWorktree(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, _ string, branchName string) (bool, error) {
			if state.branchInUseErr != nil {
				return false, state.branchInUseErr
			}
			return state.branchInUse[branchName], nil
		},
	).Maybe()
	client.EXPECT().CreateTaskWorkspace(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task) error {
			state.createdTask = cloneTask(task)
			return state.createErr
		},
	).Maybe()
	client.EXPECT().CreateTaskWorkspaceFromBranch(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task) error {
			state.createdTask = cloneTask(task)
			return state.createErr
		},
	).Maybe()
	client.EXPECT().CreateTaskWorkspaceFromPullRequest(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task, pullRequestNumber int) error {
			state.createdTask = cloneTask(task)
			state.createdPRNumber = pullRequestNumber
			return state.createErr
		},
	).Maybe()
	client.EXPECT().RemoveTaskWorkspace(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task) error {
			state.removedTask = cloneTask(task)
			return state.removeErr
		},
	).Maybe()
}

func configurePullRequestClientMock(client *MockPullRequestClient, state *pullRequestClientState) {
	client.EXPECT().HealthCheck(mock.Anything).RunAndReturn(
		func(context.Context) error {
			return state.healthErr
		},
	).Maybe()
	client.EXPECT().ListRepoPullRequests(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, repoRoot string) ([]RepoPullRequest, error) {
			state.lastListRepoRoot = repoRoot
			if state.listErr != nil {
				return nil, state.listErr
			}
			return append([]RepoPullRequest(nil), state.listRepoPullRequests...), nil
		},
	).Maybe()
	client.EXPECT().CheckPullRequestStatus(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, repoRoot string, branchName string) (*PRStatus, error) {
			state.lastStatusRepoRoot = repoRoot
			state.checkStatusCalls++
			if state.checkStatusErr != nil {
				return nil, state.checkStatusErr
			}
			if len(state.statusSequence) > 0 {
				status := state.statusSequence[0]
				state.statusSequence = state.statusSequence[1:]
				return clonePRStatus(status), nil
			}
			if state.statusByBranch == nil {
				return &PRStatus{State: PRStateNone}, nil
			}
			return clonePRStatus(state.statusByBranch[branchName]), nil
		},
	).Maybe()
}

func configureTmuxSessionMock(client *MockTmuxSessionClient, state *sessionClientState) {
	client.EXPECT().HealthCheck(mock.Anything).RunAndReturn(
		func(context.Context) error {
			return state.healthErr
		},
	).Maybe()
	client.EXPECT().StartTaskSession(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task, launch TaskSessionLaunchSpec) error {
			state.startedTask = cloneTask(task)
			state.startedLaunch = launch
			return state.startErr
		},
	).Maybe()
	client.EXPECT().AttachTaskSession(mock.Anything, mock.Anything).Return(nil).Maybe()
	client.EXPECT().InspectTaskSession(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, _ *Task) (TaskSessionRuntimeState, error) {
			return state.inspectState, state.inspectErr
		},
	).Maybe()
	client.EXPECT().DeleteTaskSession(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task) error {
			if state.deleteErr != nil {
				return state.deleteErr
			}
			state.deletedTask = cloneTask(task)
			return nil
		},
	).Maybe()
}

func configureProviderClientMock(client *MockProviderClient, state *providerClientState) {
	client.EXPECT().HealthCheck(mock.Anything).RunAndReturn(
		func(context.Context) error {
			return state.healthErr
		},
	).Maybe()
	client.EXPECT().SuggestTaskName(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, string) (TaskSuggestion, error) {
			if state.suggestErr != nil {
				return TaskSuggestion{}, state.suggestErr
			}
			if state.suggestedSuggestion.Name != "" {
				return state.suggestedSuggestion, nil
			}
			return TaskSuggestion{Name: state.suggestedName, BranchType: "feat"}, nil
		},
	).Maybe()
	client.EXPECT().EnsureTaskSessionEnvironment(mock.Anything).RunAndReturn(
		func(context.Context) error {
			state.sessionEnvCalls++
			return state.sessionEnvErr
		},
	).Maybe()
	client.EXPECT().BuildWorkspaceBootstrapSpec(mock.Anything).RunAndReturn(
		func(task *Task) (WorkspaceBootstrapSpec, error) {
			state.bootstrapRequest = cloneTask(task)
			if state.bootstrapErr != nil {
				return WorkspaceBootstrapSpec{}, state.bootstrapErr
			}
			return state.bootstrapSpec, nil
		},
	).Maybe()
	client.EXPECT().BuildTaskSessionLaunchSpec(mock.Anything).RunAndReturn(
		func(task *Task) (TaskSessionLaunchSpec, error) {
			if state.launchErr != nil {
				return TaskSessionLaunchSpec{}, state.launchErr
			}
			if hasCustomLaunchSpec(state.launchRequest) {
				return state.launchRequest, nil
			}
			return TaskSessionLaunchSpec{
				Command:      []string{"codex"},
				ReadyMarker:  "›",
				PrefillInput: []string{task.Prompt},
			}, nil
		},
	).Maybe()
	client.EXPECT().BuildReconnectTaskSessionLaunchSpec(mock.Anything, mock.Anything).RunAndReturn(
		func(_ *Task, sessionID string) (TaskSessionLaunchSpec, error) {
			if state.reconnectLaunchErr != nil {
				return TaskSessionLaunchSpec{}, state.reconnectLaunchErr
			}
			if hasCustomLaunchSpec(state.reconnectLaunch) {
				return state.reconnectLaunch, nil
			}
			return TaskSessionLaunchSpec{
				Command:     []string{"codex", "resume", sessionID},
				ReadyMarker: "›",
			}, nil
		},
	).Maybe()
	client.EXPECT().TaskSessionCommandName().Return("codex").Maybe()
	client.EXPECT().HookEventToTaskStatus(mock.Anything).RunAndReturn(
		func(input HookEventInput) (*TaskStatusUpdate, error) {
			state.hookInput = input
			if state.hookErr != nil {
				return nil, state.hookErr
			}
			if state.hookUpdate == nil {
				return nil, nil
			}
			update := *state.hookUpdate
			return &update, nil
		},
	).Maybe()
	client.EXPECT().RecoverLatestTaskStatus(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, current TaskStatusUpdate, sessions []TaskProviderSession) (*TaskStatusUpdate, error) {
			copyCurrent := current
			state.statusRecoveryCurrent = &copyCurrent
			state.statusRecoverySessions = append([]TaskProviderSession(nil), sessions...)
			if state.statusRecoveryErr != nil {
				return nil, state.statusRecoveryErr
			}
			if state.statusRecoveryUpdate == nil {
				return nil, nil
			}
			update := *state.statusRecoveryUpdate
			return &update, nil
		},
	).Maybe()
	client.EXPECT().ReadSessionActivity(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, session TaskProviderSession, after time.Time) ([]TaskActivityEvent, error) {
			transcriptPath := strings.TrimSpace(session.TranscriptPath)
			state.activityCalls = append(state.activityCalls, providerActivityCall{
				transcriptPath: transcriptPath,
				after:          after,
			})
			if state.activityErrByTranscript != nil && state.activityErrByTranscript[transcriptPath] != nil {
				return nil, state.activityErrByTranscript[transcriptPath]
			}
			if state.activityByTranscript == nil {
				return nil, nil
			}
			events := state.activityByTranscript[transcriptPath]
			return append([]TaskActivityEvent(nil), events...), nil
		},
	).Maybe()
	client.EXPECT().ReadSessionTokenUsage(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, transcriptPath string) (*SessionTokenUsage, error) {
			state.tokenUsageCalls = append(state.tokenUsageCalls, providerTokenUsageCall{
				transcriptPath: transcriptPath,
			})
			if state.errByTranscript != nil && state.errByTranscript[transcriptPath] != nil {
				return nil, state.errByTranscript[transcriptPath]
			}
			if state.usageByTranscript == nil {
				return nil, nil
			}
			usage := state.usageByTranscript[transcriptPath]
			if usage == nil {
				return nil, nil
			}
			copy := *usage
			return &copy, nil
		},
	).Maybe()
}

func configureWorkspaceManagerMock(
	workspace *MockTaskWorkspaceManager,
	state *workspaceManagerState,
	session *sessionClientState,
) {
	workspace.EXPECT().SetupTaskWorkspace(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task, repoRoot string) error {
			state.setupCalled = true
			state.repoRoot = repoRoot
			if task != nil {
				state.worktreePath = task.WorktreePath
				state.preparedDisplayName = task.DisplayName
				state.preparedBranchName = task.BranchName
			}
			state.setupCalledBeforeSession = session.startedTask == nil
			return state.setupErr
		},
	).Maybe()
	workspace.EXPECT().BootstrapTaskWorkspace(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task, bootstrapSpec WorkspaceBootstrapSpec) error {
			state.bootstrapCalled = true
			state.bootstrapSpec = bootstrapSpec
			if task != nil {
				state.worktreePath = task.WorktreePath
				state.preparedDisplayName = task.DisplayName
				state.preparedBranchName = task.BranchName
			}
			state.bootstrapCalledBeforeSession = session.startedTask == nil
			return state.bootstrapErr
		},
	).Maybe()
}

func configureTaskRepositoryMock(repo *MockTaskRepository, state *taskRepositoryState) {
	repo.EXPECT().HealthCheck(mock.Anything).RunAndReturn(
		func(context.Context) error {
			return state.healthErr
		},
	).Maybe()
	repo.EXPECT().CreateTask(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task) error {
			if state.createErr != nil {
				return state.createErr
			}
			state.createdTask = cloneTask(task)
			return nil
		},
	).Maybe()
	repo.EXPECT().UpdateTask(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task) error {
			state.updateCount++
			if state.updateErr != nil &&
				(state.updateErrAt == 0 || state.updateCount == state.updateErrAt) {
				return state.updateErr
			}
			state.updatedTask = cloneTask(task)
			return nil
		},
	).Maybe()
	repo.EXPECT().DeleteTask(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) error {
			if state.deleteErr != nil {
				return state.deleteErr
			}
			state.deletedTaskID = taskID
			filtered := state.listTasks[:0]
			for _, task := range state.listTasks {
				if task == nil || task.ID == taskID {
					continue
				}
				filtered = append(filtered, cloneTask(task))
			}
			state.listTasks = filtered
			state.mu.Lock()
			delete(state.latestByTask, taskID)
			state.mu.Unlock()
			return nil
		},
	).Maybe()
	repo.EXPECT().ListTasks(mock.Anything).RunAndReturn(
		func(context.Context) ([]*Task, error) {
			tasks := make([]*Task, 0, len(state.listTasks))
			for _, task := range state.listTasks {
				tasks = append(tasks, cloneTask(task))
			}
			return tasks, nil
		},
	).Maybe()
	repo.EXPECT().RecordTaskActivity(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, event TaskActivityEvent) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.activityByTask[event.TaskID] = append(state.activityByTask[event.TaskID], event)
			return nil
		},
	).Maybe()
	repo.EXPECT().GetTaskActivity(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string, limit int) ([]TaskActivityEvent, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			events := append([]TaskActivityEvent(nil), state.activityByTask[taskID]...)
			if limit > 0 && len(events) > limit {
				events = events[len(events)-limit:]
			}
			return events, nil
		},
	).Maybe()
	repo.EXPECT().UpsertTaskStatus(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, update TaskStatusUpdate) error {
			state.mu.Lock()
			state.latestByTask[update.TaskID] = update
			subscribers := append([]chan TaskStatusUpdate(nil), state.subscribers[update.TaskID]...)
			state.mu.Unlock()
			for _, subscriber := range subscribers {
				subscriber <- update
			}
			return nil
		},
	).Maybe()
	repo.EXPECT().UpsertTaskResumeMetadata(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, metadata TaskResumeMetadata) error {
			if state.resumeMetadataErr != nil {
				return state.resumeMetadataErr
			}
			copy := metadata
			state.savedResumeMetadata = &copy
			state.mu.Lock()
			state.latestResumeByTask[metadata.TaskID] = metadata
			state.mu.Unlock()
			return nil
		},
	).Maybe()
	repo.EXPECT().UpsertTaskProviderSession(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, session TaskProviderSession) error {
			state.savedProviderSessions = append(state.savedProviderSessions, session)
			state.providerSessionsByTask[session.TaskID] = append(
				state.providerSessionsByTask[session.TaskID],
				session,
			)
			return nil
		},
	).Maybe()
	repo.EXPECT().LatestTaskStatus(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) (*TaskStatusUpdate, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			update, ok := state.latestByTask[taskID]
			if !ok {
				return nil, nil
			}
			copy := update
			return &copy, nil
		},
	).Maybe()
	repo.EXPECT().LatestTaskResumeMetadata(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) (*TaskResumeMetadata, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			metadata, ok := state.latestResumeByTask[taskID]
			if !ok {
				return nil, nil
			}
			copy := metadata
			return &copy, nil
		},
	).Maybe()
	repo.EXPECT().ListTaskProviderSessions(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) ([]TaskProviderSession, error) {
			return append([]TaskProviderSession(nil), state.providerSessionsByTask[taskID]...), nil
		},
	).Maybe()
	repo.EXPECT().SubscribeTaskStatus(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error) {
			updates := make(chan TaskStatusUpdate, 8)
			state.mu.Lock()
			state.subscribers[taskID] = append(state.subscribers[taskID], updates)
			state.mu.Unlock()
			var once sync.Once
			cleanup := func() {
				once.Do(func() {
					state.mu.Lock()
					defer state.mu.Unlock()
					subscribers := state.subscribers[taskID]
					filtered := subscribers[:0]
					for _, subscriber := range subscribers {
						if subscriber != updates {
							filtered = append(filtered, subscriber)
						}
					}
					if len(filtered) == 0 {
						delete(state.subscribers, taskID)
					} else {
						state.subscribers[taskID] = filtered
					}
					close(updates)
				})
			}
			go func() {
				<-ctx.Done()
				cleanup()
			}()
			return updates, nil
		},
	).Maybe()
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
