package core

import (
	"context"
	"time"

	"agent/internal/pkg/timeutil"
)

type testServiceHarness struct {
	service         *Service
	taskRepo        *fakeTaskRepository
	repoClient      *fakeRepoClient
	sessionClient   *fakeSessionClient
	providerRepo    *fakeProviderClient
	configRepo      *fakeRepoConfigRepository
	workspaceSeeder *fakeWorkspaceSeeder
}

func newTestService() *testServiceHarness {
	taskRepo := &fakeTaskRepository{}
	repoClient := &fakeRepoClient{
		repoContext: RepoContext{
			Root:       "/tmp/repo",
			Name:       "repo",
			BaseBranch: "main",
		},
	}
	sessionClient := &fakeSessionClient{}
	providerRepo := &fakeProviderClient{}
	configRepo := &fakeRepoConfigRepository{}
	workspaceSeeder := &fakeWorkspaceSeeder{}
	sessionClient.startHook = func() {
		workspaceSeeder.seededBeforeSession = workspaceSeeder.seedCalled
	}

	return &testServiceHarness{
		service: newServiceWithPorts(
			taskRepo,
			repoClient,
			sessionClient,
			map[string]ProviderClient{
				"codex": providerRepo,
			},
			configRepo,
			workspaceSeeder,
			fakeClock{
				now: time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC),
			},
			Config{
				Provider: "codex",
			},
		),
		taskRepo:        taskRepo,
		repoClient:      repoClient,
		sessionClient:   sessionClient,
		providerRepo:    providerRepo,
		configRepo:      configRepo,
		workspaceSeeder: workspaceSeeder,
	}
}

type fakeTaskRepository struct {
	isAvailableErr error
	createErr      error
	updateErr      error
	updateErrAt    int
	updateCount    int
	listTasks      []*Task
	getTask        *Task
	createdTask    *Task
	updatedTask    *Task
	appendedEvents []fakeTaskEvent
}

func (f *fakeTaskRepository) IsAvailable(context.Context) error { return f.isAvailableErr }

func (f *fakeTaskRepository) CreateTask(_ context.Context, task *Task) error {
	if f.createErr != nil {
		return f.createErr
	}

	clone := *task
	f.createdTask = &clone
	f.getTask = &clone
	return nil
}

func (f *fakeTaskRepository) UpdateTask(_ context.Context, task *Task) error {
	f.updateCount++
	if f.updateErr != nil && (f.updateErrAt == 0 || f.updateCount == f.updateErrAt) {
		return f.updateErr
	}

	clone := *task
	f.updatedTask = &clone
	f.getTask = &clone
	return nil
}

func (f *fakeTaskRepository) GetTask(context.Context, string) (*Task, error) {
	if f.getTask == nil {
		return nil, ErrTaskNotFound
	}

	clone := *f.getTask
	return &clone, nil
}

func (f *fakeTaskRepository) ListTasks(context.Context) ([]*Task, error) {
	tasks := make([]*Task, 0, len(f.listTasks))
	for _, task := range f.listTasks {
		clone := *task
		tasks = append(tasks, &clone)
	}

	return tasks, nil
}

func (f *fakeTaskRepository) AppendEvent(_ context.Context, taskID, eventType, payload string) error {
	f.appendedEvents = append(f.appendedEvents, fakeTaskEvent{
		taskID:    taskID,
		eventType: eventType,
		payload:   payload,
	})
	return nil
}

type fakeTaskEvent struct {
	taskID    string
	eventType string
	payload   string
}

type fakeRepoClient struct {
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

func (f *fakeRepoClient) IsAvailable(context.Context) error { return f.isAvailableErr }

func (f *fakeRepoClient) DetectRepo(context.Context, string) (RepoContext, error) {
	if f.detectRepoErr != nil {
		return RepoContext{}, f.detectRepoErr
	}

	return f.repoContext, nil
}

func (f *fakeRepoClient) CreateTaskWorkspace(_ context.Context, task *Task) error {
	clone := *task
	f.createdTask = &clone
	if f.createErr != nil {
		return f.createErr
	}

	f.repoResources.WorktreeExists = true
	f.repoResources.BranchExists = true
	return nil
}

func (f *fakeRepoClient) RemoveTaskWorkspace(_ context.Context, task *Task) error {
	clone := *task
	f.removedTasks = append(f.removedTasks, &clone)
	if f.removeHook != nil {
		f.removeHook(task)
	}
	if f.removeErr != nil {
		return f.removeErr
	}

	f.repoResources.WorktreeExists = false
	return nil
}

func (f *fakeRepoClient) InspectTaskWorkspace(context.Context, *Task) (RepoResources, error) {
	return f.repoResources, f.inspectErr
}

type fakeSessionClient struct {
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

func (f *fakeSessionClient) IsAvailable(context.Context) error { return f.isAvailableErr }

func (f *fakeSessionClient) StartTaskSession(_ context.Context, task *Task, launch LaunchRequest) error {
	clone := *task
	f.startedTask = &clone
	f.startedLaunch = launch
	if f.startHook != nil {
		f.startHook()
	}
	if f.startErr != nil {
		return f.startErr
	}

	f.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	return nil
}

func (f *fakeSessionClient) OpenTaskSession(_ context.Context, task *Task) error {
	clone := *task
	f.openedTask = &clone
	return f.openErr
}

func (f *fakeSessionClient) DeleteTaskSession(_ context.Context, task *Task) error {
	clone := *task
	f.deletedTasks = append(f.deletedTasks, &clone)
	if f.deleteHook != nil {
		f.deleteHook(task)
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}

	f.sessionResources = SessionResources{}
	return nil
}

func (f *fakeSessionClient) InspectTaskSession(context.Context, *Task) (SessionResources, error) {
	return f.sessionResources, f.inspectErr
}

func (f *fakeSessionClient) SnapshotTaskSession(context.Context, *Task) (RuntimeSnapshot, error) {
	return f.snapshot, f.snapshotErr
}

type fakeProviderClient struct {
	isAvailableErr error
	suggestErr     error
	suggestedName  string
	launchErr      error
	launchRequest  LaunchRequest
	runtimeState   RuntimeState
}

func (f *fakeProviderClient) IsAvailable(context.Context) error { return f.isAvailableErr }

func (f *fakeProviderClient) SuggestTaskName(context.Context, string) (string, error) {
	if f.suggestErr != nil {
		return "", f.suggestErr
	}

	return f.suggestedName, nil
}

func (f *fakeProviderClient) LaunchRequest(task *Task) (LaunchRequest, error) {
	if f.launchErr != nil {
		return LaunchRequest{}, f.launchErr
	}
	if len(f.launchRequest.Command) > 0 || len(f.launchRequest.InitialInput) > 0 || f.launchRequest.Prompt != "" {
		return f.launchRequest, nil
	}

	return LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{task.Prompt},
	}, nil
}

func (f *fakeProviderClient) DetectRuntimeState(RuntimeSnapshot) RuntimeState {
	return f.runtimeState
}

type fakeClock struct {
	now time.Time
}

func (f fakeClock) Now() time.Time { return f.now }

var _ timeutil.Clock = fakeClock{}

type fakeRepoConfigRepository struct {
	repoConfig     RepoConfig
	loadErr        error
	loadedRepoRoot string
}

func (f *fakeRepoConfigRepository) LoadRepoConfig(_ context.Context, repoRoot string) (RepoConfig, error) {
	f.loadedRepoRoot = repoRoot
	if f.loadErr != nil {
		return RepoConfig{}, f.loadErr
	}

	return f.repoConfig, nil
}

type fakeWorkspaceSeeder struct {
	validateErr         error
	seedErr             error
	validateRepoRoot    string
	validatePaths       []string
	seedInput           SeedWorkspaceInput
	seededPaths         []string
	seedCalled          bool
	seededBeforeSession bool
}

func (f *fakeWorkspaceSeeder) SeedWorkspace(_ context.Context, in SeedWorkspaceInput, progress func(string)) error {
	f.seedCalled = true
	f.seedInput = in
	if f.seedErr != nil {
		return f.seedErr
	}

	for _, path := range in.RelativePaths {
		f.seededPaths = append(f.seededPaths, path)
		if progress != nil {
			progress(path)
		}
	}

	return nil
}

func (f *fakeWorkspaceSeeder) ValidateSeedPaths(_ context.Context, repoRoot string, relativePaths []string) error {
	f.validateRepoRoot = repoRoot
	f.validatePaths = append([]string(nil), relativePaths...)
	return f.validateErr
}
