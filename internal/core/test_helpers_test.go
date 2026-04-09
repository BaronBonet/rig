package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type testServiceHarness struct {
	service *Service

	taskRepoMock *MockTaskRepository
	taskRepo     taskRepositoryState

	repoClientMock *MockRepoClient
	repoClient     repoClientState

	sessionClientMock *MockSessionClient
	sessionClient     sessionClientState

	providerRepoMock *MockProviderClient
	providerRepo     providerClientState

	configRepoMock *MockRepoConfigLoader
	configRepo     repoConfigLoaderState

	workspaceSeederMock *MockWorkspaceSeeder
	workspaceSeeder     workspaceSeederState

	workspaceBootstrapperMock *MockTaskWorkspaceBootstrapper
	workspaceBootstrapper     workspaceBootstrapperState
}

type taskRepositoryState struct {
	isAvailableErr error
	createErr      error
	updateErr      error
	updateErrAt    int
	updateCount    int
	listTasks      []*Task
	getTask        *Task
	createdTask    *Task
	updatedTask    *Task
	appendedEvents []testTaskEvent
}

type testTaskEvent struct {
	taskID    string
	eventType string
	payload   string
}

type repoClientState struct {
	isAvailableErr error
	detectRepoErr  error
	createErr      error
	inspectErr     error
	removeErr      error
	removeHook     func(*Task)
	repoContext    RepoContext
	repoResources  RepoResources
	createdTask    *Task
	removedTasks   []*Task
}

type sessionClientState struct {
	isAvailableErr   error
	startErr         error
	startHook        func()
	deleteErr        error
	deleteHook       func(*Task)
	inspectErr       error
	openErr          error
	startedTask      *Task
	openedTask       *Task
	deletedTasks     []*Task
	startedLaunch    LaunchRequest
	sessionResources SessionResources
	snapshot         RuntimeSnapshot
	snapshotErr      error
}

type providerClientState struct {
	isAvailableErr error
	suggestErr     error
	suggestedName  string
	launchErr      error
	launchRequest  LaunchRequest
	runtimeState   RuntimeState
}

type repoConfigLoaderState struct {
	repoConfig     RepoConfig
	loadErr        error
	loadedRepoRoot string
}

type workspaceSeederState struct {
	validateErr         error
	seedErr             error
	validateRepoRoot    string
	validatePaths       []string
	seedInput           SeedWorkspaceInput
	seededPaths         []string
	seedCalled          bool
	seededBeforeSession bool
}

type workspaceBootstrapperState struct {
	bootstrapErr           error
	bootstrappedTask       *Task
	bootstrappedBeforeTmux bool
}

func newTestService(t *testing.T) *testServiceHarness {
	t.Helper()

	h := &testServiceHarness{
		taskRepoMock:              NewMockTaskRepository(t),
		repoClientMock:            NewMockRepoClient(t),
		sessionClientMock:         NewMockSessionClient(t),
		providerRepoMock:          NewMockProviderClient(t),
		configRepoMock:            NewMockRepoConfigLoader(t),
		workspaceSeederMock:       NewMockWorkspaceSeeder(t),
		workspaceBootstrapperMock: NewMockTaskWorkspaceBootstrapper(t),
		repoClient: repoClientState{
			repoContext: RepoContext{
				Root:       "/tmp/repo",
				Name:       "repo",
				BaseBranch: "main",
			},
		},
	}

	h.sessionClient.startHook = func() {
		h.workspaceSeeder.seededBeforeSession = h.workspaceSeeder.seedCalled
	}

	wireTaskRepositoryMock(h)
	wireRepoClientMock(h)
	wireSessionClientMock(h)
	wireProviderClientMock(h)
	wireRepoConfigLoaderMock(h)
	wireWorkspaceSeederMock(h)
	wireWorkspaceBootstrapperMock(h)

	h.service = NewService(
		h.taskRepoMock,
		nil,
		nil,
		h.repoClientMock,
		h.sessionClientMock,
		map[string]ProviderClient{
			"codex": h.providerRepoMock,
		},
		h.configRepoMock,
		h.workspaceSeederMock,
		h.workspaceBootstrapperMock,
		Config{Provider: "codex"},
	)

	return h
}

func wireTaskRepositoryMock(h *testServiceHarness) {
	h.taskRepoMock.EXPECT().IsAvailable(mock.Anything).RunAndReturn(func(context.Context) error {
		return h.taskRepo.isAvailableErr
	}).Maybe()
	h.taskRepoMock.EXPECT().CreateTask(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			if h.taskRepo.createErr != nil {
				return h.taskRepo.createErr
			}

			h.taskRepo.createdTask = cloneTask(task)
			h.taskRepo.getTask = cloneTask(task)
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
			h.taskRepo.getTask = cloneTask(task)
			return nil
		}).Maybe()
	h.taskRepoMock.EXPECT().GetTask(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, string) (*Task, error) {
			if h.taskRepo.getTask == nil {
				return nil, ErrTaskNotFound
			}

			return cloneTask(h.taskRepo.getTask), nil
		}).Maybe()
	h.taskRepoMock.EXPECT().ListTasks(mock.Anything).RunAndReturn(func(context.Context) ([]*Task, error) {
		tasks := make([]*Task, 0, len(h.taskRepo.listTasks))
		for _, task := range h.taskRepo.listTasks {
			tasks = append(tasks, cloneTask(task))
		}

		return tasks, nil
	}).Maybe()
	h.taskRepoMock.EXPECT().AppendEvent(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, taskID, eventType, payload string) error {
			h.taskRepo.appendedEvents = append(h.taskRepo.appendedEvents, testTaskEvent{
				taskID:    taskID,
				eventType: eventType,
				payload:   payload,
			})
			return nil
		}).Maybe()
}

func wireRepoClientMock(h *testServiceHarness) {
	h.repoClientMock.EXPECT().IsAvailable(mock.Anything).RunAndReturn(func(context.Context) error {
		return h.repoClient.isAvailableErr
	}).Maybe()
	h.repoClientMock.EXPECT().DetectRepo(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, string) (RepoContext, error) {
			if h.repoClient.detectRepoErr != nil {
				return RepoContext{}, h.repoClient.detectRepoErr
			}

			return h.repoClient.repoContext, nil
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
	h.repoClientMock.EXPECT().RemoveTaskWorkspace(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.repoClient.removedTasks = append(h.repoClient.removedTasks, cloneTask(task))
			if h.repoClient.removeHook != nil {
				h.repoClient.removeHook(task)
			}
			if h.repoClient.removeErr != nil {
				return h.repoClient.removeErr
			}

			h.repoClient.repoResources.WorktreeExists = false
			return nil
		}).Maybe()
	h.repoClientMock.EXPECT().InspectTaskWorkspace(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, *Task) (RepoResources, error) {
			return h.repoClient.repoResources, h.repoClient.inspectErr
		}).Maybe()
}

func wireSessionClientMock(h *testServiceHarness) {
	h.sessionClientMock.EXPECT().IsAvailable(mock.Anything).RunAndReturn(func(context.Context) error {
		return h.sessionClient.isAvailableErr
	}).Maybe()
	h.sessionClientMock.EXPECT().StartTaskSession(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task, launch LaunchRequest) error {
			h.sessionClient.startedTask = cloneTask(task)
			h.sessionClient.startedLaunch = launch
			if h.sessionClient.startHook != nil {
				h.sessionClient.startHook()
			}
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
	h.sessionClientMock.EXPECT().OpenTaskSession(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.sessionClient.openedTask = cloneTask(task)
			return h.sessionClient.openErr
		}).Maybe()
	h.sessionClientMock.EXPECT().DeleteTaskSession(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.sessionClient.deletedTasks = append(h.sessionClient.deletedTasks, cloneTask(task))
			if h.sessionClient.deleteHook != nil {
				h.sessionClient.deleteHook(task)
			}
			if h.sessionClient.deleteErr != nil {
				return h.sessionClient.deleteErr
			}

			h.sessionClient.sessionResources = SessionResources{}
			return nil
		}).Maybe()
	h.sessionClientMock.EXPECT().InspectTaskSession(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, *Task) (SessionResources, error) {
			return h.sessionClient.sessionResources, h.sessionClient.inspectErr
		}).Maybe()
	h.sessionClientMock.EXPECT().SnapshotTaskSession(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, *Task) (RuntimeSnapshot, error) {
			return h.sessionClient.snapshot, h.sessionClient.snapshotErr
		}).Maybe()
}

func wireProviderClientMock(h *testServiceHarness) {
	h.providerRepoMock.EXPECT().IsAvailable(mock.Anything).RunAndReturn(func(context.Context) error {
		return h.providerRepo.isAvailableErr
	}).Maybe()
	h.providerRepoMock.EXPECT().SuggestTaskName(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, string) (string, error) {
			if h.providerRepo.suggestErr != nil {
				return "", h.providerRepo.suggestErr
			}

			return h.providerRepo.suggestedName, nil
		}).Maybe()
	h.providerRepoMock.EXPECT().LaunchRequest(mock.Anything).RunAndReturn(func(task *Task) (LaunchRequest, error) {
		if h.providerRepo.launchErr != nil {
			return LaunchRequest{}, h.providerRepo.launchErr
		}
		if hasCustomLaunchRequest(h.providerRepo.launchRequest) {
			return h.providerRepo.launchRequest, nil
		}

		return LaunchRequest{
			Command:      []string{"codex"},
			Prompt:       "›",
			InitialInput: []string{task.Prompt},
		}, nil
	}).Maybe()
	h.providerRepoMock.EXPECT().DetectRuntimeState(mock.Anything).RunAndReturn(func(RuntimeSnapshot) RuntimeState {
		return h.providerRepo.runtimeState
	}).Maybe()
}

func wireRepoConfigLoaderMock(h *testServiceHarness) {
	h.configRepoMock.EXPECT().LoadRepoConfig(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, repoRoot string) (RepoConfig, error) {
			h.configRepo.loadedRepoRoot = repoRoot
			if h.configRepo.loadErr != nil {
				return RepoConfig{}, h.configRepo.loadErr
			}

			return h.configRepo.repoConfig, nil
		}).Maybe()
}

func wireWorkspaceSeederMock(h *testServiceHarness) {
	h.workspaceSeederMock.EXPECT().SeedWorkspace(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in SeedWorkspaceInput, progress func(string)) error {
			h.workspaceSeeder.seedCalled = true
			h.workspaceSeeder.seedInput = in
			if h.workspaceSeeder.seedErr != nil {
				return h.workspaceSeeder.seedErr
			}

			for _, path := range in.RelativePaths {
				h.workspaceSeeder.seededPaths = append(h.workspaceSeeder.seededPaths, path)
				if progress != nil {
					progress(path)
				}
			}

			return nil
		}).Maybe()
	h.workspaceSeederMock.EXPECT().ValidateSeedPaths(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, repoRoot string, relativePaths []string) error {
			h.workspaceSeeder.validateRepoRoot = repoRoot
			h.workspaceSeeder.validatePaths = append([]string(nil), relativePaths...)
			return h.workspaceSeeder.validateErr
		}).Maybe()
}

func wireWorkspaceBootstrapperMock(h *testServiceHarness) {
	h.workspaceBootstrapperMock.On("BootstrapTaskWorkspace", mock.Anything, mock.Anything).
		Return(func(_ context.Context, task *Task) error {
			h.workspaceBootstrapper.bootstrappedTask = cloneTask(task)
			h.workspaceBootstrapper.bootstrappedBeforeTmux = h.sessionClient.startedTask == nil
			return h.workspaceBootstrapper.bootstrapErr
		}).Maybe()
}

func hasCustomLaunchRequest(req LaunchRequest) bool {
	return len(req.Command) > 0 || len(req.InitialInput) > 0 || req.Prompt != ""
}

func (h *testServiceHarness) existingTask(id string) *Task {
	task := &Task{
		ID:               id,
		Prompt:           "fix the failing test",
		DisplayName:      "failing test",
		Slug:             "failing-test",
		RepoRoot:         "/tmp/repo",
		RepoName:         "repo",
		BaseBranch:       "main",
		BranchName:       "feat/failing-test",
		WorktreePath:     "/tmp/repo-failing-test",
		TmuxSession:      "repo_failing-test",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
		Status:           TaskStatusRunning,
	}

	h.taskRepo.listTasks = []*Task{task}
	h.taskRepo.getTask = task

	return task
}

func requireTimeInWindow(t *testing.T, got, before, after time.Time) {
	t.Helper()
	require.False(t, got.IsZero())
	require.False(t, got.Before(before), "expected %s to be on or after %s", got, before)
	require.False(t, got.After(after), "expected %s to be on or before %s", got, after)
}
