package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type testServiceHarness struct {
	service         *Service
	taskRepo        *taskRepositoryHarness
	repoClient      *repoClientHarness
	sessionClient   *sessionClientHarness
	providerRepo    *providerClientHarness
	configRepo      *repoConfigRepositoryHarness
	workspaceSeeder *workspaceSeederHarness
}

func newTestService(t *testing.T) *testServiceHarness {
	t.Helper()

	taskRepo := newTaskRepositoryHarness(t)
	repoClient := newRepoClientHarness(t)
	sessionClient := newSessionClientHarness(t)
	providerRepo := newProviderClientHarness(t)
	configRepo := newRepoConfigRepositoryHarness(t)
	workspaceSeeder := newWorkspaceSeederHarness(t)

	sessionClient.startHook = func() {
		workspaceSeeder.seededBeforeSession = workspaceSeeder.seedCalled
	}

	return &testServiceHarness{
		service: NewService(
			taskRepo.MockTaskRepository,
			repoClient.MockRepoClient,
			sessionClient.MockSessionClient,
			map[string]ProviderClient{
				"codex": providerRepo.MockProviderClient,
			},
			configRepo.MockRepoConfigRepository,
			workspaceSeeder.MockWorkspaceSeeder,
			Config{Provider: "codex"},
		),
		taskRepo:        taskRepo,
		repoClient:      repoClient,
		sessionClient:   sessionClient,
		providerRepo:    providerRepo,
		configRepo:      configRepo,
		workspaceSeeder: workspaceSeeder,
	}
}

type taskRepositoryHarness struct {
	*MockTaskRepository
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

func newTaskRepositoryHarness(t *testing.T) *taskRepositoryHarness {
	t.Helper()

	h := &taskRepositoryHarness{MockTaskRepository: NewMockTaskRepository(t)}
	h.EXPECT().IsAvailable(mock.Anything).RunAndReturn(func(context.Context) error {
		return h.isAvailableErr
	}).Maybe()
	h.EXPECT().CreateTask(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, task *Task) error {
		if h.createErr != nil {
			return h.createErr
		}

		h.createdTask = cloneTask(task)
		h.getTask = cloneTask(task)
		return nil
	}).Maybe()
	h.EXPECT().UpdateTask(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, task *Task) error {
		h.updateCount++
		if h.updateErr != nil && (h.updateErrAt == 0 || h.updateCount == h.updateErrAt) {
			return h.updateErr
		}

		h.updatedTask = cloneTask(task)
		h.getTask = cloneTask(task)
		return nil
	}).Maybe()
	h.EXPECT().GetTask(mock.Anything, mock.Anything).RunAndReturn(func(context.Context, string) (*Task, error) {
		if h.getTask == nil {
			return nil, ErrTaskNotFound
		}

		return cloneTask(h.getTask), nil
	}).Maybe()
	h.EXPECT().ListTasks(mock.Anything).RunAndReturn(func(context.Context) ([]*Task, error) {
		tasks := make([]*Task, 0, len(h.listTasks))
		for _, task := range h.listTasks {
			tasks = append(tasks, cloneTask(task))
		}

		return tasks, nil
	}).Maybe()
	h.EXPECT().AppendEvent(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, taskID, eventType, payload string) error {
			h.appendedEvents = append(h.appendedEvents, testTaskEvent{
				taskID:    taskID,
				eventType: eventType,
				payload:   payload,
			})
			return nil
		}).Maybe()

	return h
}

type testTaskEvent struct {
	taskID    string
	eventType string
	payload   string
}

type repoClientHarness struct {
	*MockRepoClient
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

func newRepoClientHarness(t *testing.T) *repoClientHarness {
	t.Helper()

	h := &repoClientHarness{
		MockRepoClient: NewMockRepoClient(t),
		repoContext: RepoContext{
			Root:       "/tmp/repo",
			Name:       "repo",
			BaseBranch: "main",
		},
	}
	h.EXPECT().IsAvailable(mock.Anything).RunAndReturn(func(context.Context) error {
		return h.isAvailableErr
	}).Maybe()
	h.EXPECT().DetectRepo(mock.Anything, mock.Anything).RunAndReturn(func(context.Context, string) (RepoContext, error) {
		if h.detectRepoErr != nil {
			return RepoContext{}, h.detectRepoErr
		}

		return h.repoContext, nil
	}).Maybe()
	h.EXPECT().CreateTaskWorkspace(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.createdTask = cloneTask(task)
			if h.createErr != nil {
				return h.createErr
			}

			h.repoResources.WorktreeExists = true
			h.repoResources.BranchExists = true
			return nil
		}).Maybe()
	h.EXPECT().RemoveTaskWorkspace(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.removedTasks = append(h.removedTasks, cloneTask(task))
			if h.removeHook != nil {
				h.removeHook(task)
			}
			if h.removeErr != nil {
				return h.removeErr
			}

			h.repoResources.WorktreeExists = false
			return nil
		}).Maybe()
	h.EXPECT().InspectTaskWorkspace(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, *Task) (RepoResources, error) {
			return h.repoResources, h.inspectErr
		}).Maybe()

	return h
}

type sessionClientHarness struct {
	*MockSessionClient
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

func newSessionClientHarness(t *testing.T) *sessionClientHarness {
	t.Helper()

	h := &sessionClientHarness{MockSessionClient: NewMockSessionClient(t)}
	h.EXPECT().IsAvailable(mock.Anything).RunAndReturn(func(context.Context) error {
		return h.isAvailableErr
	}).Maybe()
	h.EXPECT().StartTaskSession(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task, launch LaunchRequest) error {
			h.startedTask = cloneTask(task)
			h.startedLaunch = launch
			if h.startHook != nil {
				h.startHook()
			}
			if h.startErr != nil {
				return h.startErr
			}

			h.sessionResources = SessionResources{
				SessionExists:      true,
				AgentWindowExists:  true,
				EditorWindowExists: true,
			}
			return nil
		}).Maybe()
	h.EXPECT().OpenTaskSession(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, task *Task) error {
		h.openedTask = cloneTask(task)
		return h.openErr
	}).Maybe()
	h.EXPECT().DeleteTaskSession(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, task *Task) error {
			h.deletedTasks = append(h.deletedTasks, cloneTask(task))
			if h.deleteHook != nil {
				h.deleteHook(task)
			}
			if h.deleteErr != nil {
				return h.deleteErr
			}

			h.sessionResources = SessionResources{}
			return nil
		}).Maybe()
	h.EXPECT().InspectTaskSession(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, *Task) (SessionResources, error) {
			return h.sessionResources, h.inspectErr
		}).Maybe()
	h.EXPECT().SnapshotTaskSession(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, *Task) (RuntimeSnapshot, error) {
			return h.snapshot, h.snapshotErr
		}).Maybe()

	return h
}

type providerClientHarness struct {
	*MockProviderClient
	isAvailableErr error
	suggestErr     error
	suggestedName  string
	launchErr      error
	launchRequest  LaunchRequest
	runtimeState   RuntimeState
}

func newProviderClientHarness(t *testing.T) *providerClientHarness {
	t.Helper()

	h := &providerClientHarness{MockProviderClient: NewMockProviderClient(t)}
	h.EXPECT().IsAvailable(mock.Anything).RunAndReturn(func(context.Context) error {
		return h.isAvailableErr
	}).Maybe()
	h.EXPECT().SuggestTaskName(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, string) (string, error) {
			if h.suggestErr != nil {
				return "", h.suggestErr
			}

			return h.suggestedName, nil
		}).Maybe()
	h.EXPECT().LaunchRequest(mock.Anything).RunAndReturn(func(task *Task) (LaunchRequest, error) {
		if h.launchErr != nil {
			return LaunchRequest{}, h.launchErr
		}
		if hasCustomLaunchRequest(h.launchRequest) {
			return h.launchRequest, nil
		}

		return LaunchRequest{
			Command:      []string{"codex"},
			Prompt:       "›",
			InitialInput: []string{task.Prompt},
		}, nil
	}).Maybe()
	h.EXPECT().DetectRuntimeState(mock.Anything).RunAndReturn(func(RuntimeSnapshot) RuntimeState {
		return h.runtimeState
	}).Maybe()

	return h
}

type repoConfigRepositoryHarness struct {
	*MockRepoConfigRepository
	repoConfig     RepoConfig
	loadErr        error
	loadedRepoRoot string
}

func newRepoConfigRepositoryHarness(t *testing.T) *repoConfigRepositoryHarness {
	t.Helper()

	h := &repoConfigRepositoryHarness{MockRepoConfigRepository: NewMockRepoConfigRepository(t)}
	h.EXPECT().LoadRepoConfig(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, repoRoot string) (RepoConfig, error) {
			h.loadedRepoRoot = repoRoot
			if h.loadErr != nil {
				return RepoConfig{}, h.loadErr
			}

			return h.repoConfig, nil
		}).Maybe()

	return h
}

type workspaceSeederHarness struct {
	*MockWorkspaceSeeder
	validateErr         error
	seedErr             error
	validateRepoRoot    string
	validatePaths       []string
	seedInput           SeedWorkspaceInput
	seededPaths         []string
	seedCalled          bool
	seededBeforeSession bool
}

func newWorkspaceSeederHarness(t *testing.T) *workspaceSeederHarness {
	t.Helper()

	h := &workspaceSeederHarness{MockWorkspaceSeeder: NewMockWorkspaceSeeder(t)}
	h.EXPECT().SeedWorkspace(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in SeedWorkspaceInput, progress func(string)) error {
			h.seedCalled = true
			h.seedInput = in
			if h.seedErr != nil {
				return h.seedErr
			}

			for _, path := range in.RelativePaths {
				h.seededPaths = append(h.seededPaths, path)
				if progress != nil {
					progress(path)
				}
			}

			return nil
		}).Maybe()
	h.EXPECT().ValidateSeedPaths(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, repoRoot string, relativePaths []string) error {
			h.validateRepoRoot = repoRoot
			h.validatePaths = append([]string(nil), relativePaths...)
			return h.validateErr
		}).Maybe()

	return h
}

func hasCustomLaunchRequest(req LaunchRequest) bool {
	return len(req.Command) > 0 || len(req.InitialInput) > 0 || req.Prompt != ""
}

func requireTimeInWindow(t *testing.T, got, before, after time.Time) {
	t.Helper()
	require.False(t, got.IsZero())
	require.False(t, got.Before(before), "expected %s to be on or after %s", got, before)
	require.False(t, got.After(after), "expected %s to be on or before %s", got, after)
}
