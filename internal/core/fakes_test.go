package core

import (
	"context"
	"os"
	"time"

	"agent/internal/pkg/timeutil"
)

type testServiceHarness struct {
	service         *Service
	taskRepo        *fakeTaskRepository
	gitRepo         *fakeGitRepository
	tmuxRepo        *fakeTmuxRepository
	providerRepo    *fakeProviderRepository
	runtimeMonitor  *fakeRuntimeMonitor
	runtimeDetector *fakeRuntimeStateDetector
	configRepo      *fakeRepoConfigRepository
	workspaceSeeder *fakeWorkspaceSeeder
}

func newTestService() *testServiceHarness {
	taskRepo := &fakeTaskRepository{}
	gitRepo := &fakeGitRepository{
		repoContext: RepoContext{
			Root:       "/tmp/repo",
			Name:       "repo",
			BaseBranch: "main",
		},
	}
	tmuxRepo := &fakeTmuxRepository{}
	providerRepo := &fakeProviderRepository{}
	runtimeMonitor := &fakeRuntimeMonitor{}
	runtimeDetector := &fakeRuntimeStateDetector{}
	configRepo := &fakeRepoConfigRepository{}
	workspaceSeeder := &fakeWorkspaceSeeder{}
	tmuxRepo.createSessionHook = func() {
		workspaceSeeder.seededBeforeTmux = workspaceSeeder.seedCalled
	}

	return &testServiceHarness{
		service: NewService(taskRepo, gitRepo, tmuxRepo, map[string]ProviderRepository{
			"codex": providerRepo,
		}, runtimeMonitor, map[string]RuntimeStateDetector{
			"codex": runtimeDetector,
		}, configRepo, workspaceSeeder, fakeClock{
			now: time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC),
		}, Config{
			DatabasePath: "/tmp/agent/state.db",
			Provider:     "codex",
		}),
		taskRepo:        taskRepo,
		gitRepo:         gitRepo,
		tmuxRepo:        tmuxRepo,
		providerRepo:    providerRepo,
		runtimeMonitor:  runtimeMonitor,
		runtimeDetector: runtimeDetector,
		configRepo:      configRepo,
		workspaceSeeder: workspaceSeeder,
	}
}

type fakeTaskRepository struct {
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

type fakeGitRepository struct {
	isAvailableErr      error
	detectRepoErr       error
	createWorktreeErr   error
	removeWorktreeErr   error
	removeWorktreeHook  func(string)
	branchExists        bool
	repoContext         RepoContext
	createWorktreeInput CreateWorktreeInput
	removedWorktrees    []string
}

func (f *fakeGitRepository) IsAvailable(context.Context) error { return f.isAvailableErr }
func (f *fakeGitRepository) DetectRepo(context.Context, string) (RepoContext, error) {
	if f.detectRepoErr != nil {
		return RepoContext{}, f.detectRepoErr
	}

	return f.repoContext, nil
}
func (f *fakeGitRepository) BranchExists(context.Context, string, string) (bool, error) {
	return f.branchExists, nil
}
func (f *fakeGitRepository) CreateWorktree(_ context.Context, input CreateWorktreeInput) error {
	f.createWorktreeInput = input
	return f.createWorktreeErr
}

func (f *fakeGitRepository) RemoveWorktree(_ context.Context, _ string, path string) error {
	f.removedWorktrees = append(f.removedWorktrees, path)
	if f.removeWorktreeHook != nil {
		f.removeWorktreeHook(path)
	}
	if f.removeWorktreeErr != nil {
		return f.removeWorktreeErr
	}

	return os.RemoveAll(path)
}

type fakeTmuxRepository struct {
	isAvailableErr    error
	createSessionErr  error
	createSessionHook func()
	killSessionErr    error
	killSessionHook   func()
	sendKeysErr       error
	typeInErr         error
	typedCommand      []string
	sessionExists     bool
	windowExists      map[string]map[string]bool
	attachedSession   string
	createdSession    CreateSessionInput
	sentCommand       []string
	sentSession       string
	sentWindow        string
	sentWindows       []fakeTmuxWindowCommand
	killedSessions    []string
}

func (f *fakeTmuxRepository) IsAvailable(context.Context) error { return f.isAvailableErr }
func (f *fakeTmuxRepository) SessionExists(context.Context, string) (bool, error) {
	return f.sessionExists, nil
}
func (f *fakeTmuxRepository) WindowExists(_ context.Context, session, window string) (bool, error) {
	if f.windowExists == nil {
		return false, nil
	}

	windows, ok := f.windowExists[session]
	if !ok {
		return false, nil
	}

	return windows[window], nil
}
func (f *fakeTmuxRepository) CreateSession(_ context.Context, input CreateSessionInput) error {
	f.createdSession = input
	if f.createSessionHook != nil {
		f.createSessionHook()
	}
	if f.createSessionErr == nil {
		f.sessionExists = true
		if f.windowExists == nil {
			f.windowExists = make(map[string]map[string]bool)
		}

		windows := f.windowExists[input.SessionName]
		if windows == nil {
			windows = make(map[string]bool)
			f.windowExists[input.SessionName] = windows
		}

		agentWindow := input.AgentWindowName
		if agentWindow == "" {
			agentWindow = "agent"
		}
		editorWindow := input.EditorWindowName
		if editorWindow == "" {
			editorWindow = "editor"
		}

		windows[agentWindow] = true
		windows[editorWindow] = true
	}
	return f.createSessionErr
}

func (f *fakeTmuxRepository) KillSession(_ context.Context, session string) error {
	f.killedSessions = append(f.killedSessions, session)
	if f.killSessionHook != nil {
		f.killSessionHook()
	}
	if f.killSessionErr != nil {
		return f.killSessionErr
	}

	f.sessionExists = false
	if f.windowExists != nil {
		delete(f.windowExists, session)
	}
	return nil
}

func (f *fakeTmuxRepository) AttachOrSwitch(_ context.Context, session string) error {
	f.attachedSession = session
	return nil
}
func (f *fakeTmuxRepository) SendKeysToWindow(_ context.Context, session, window string, command []string) error {
	f.sentSession = session
	f.sentWindow = window
	f.sentCommand = append([]string(nil), command...)
	f.sentWindows = append(f.sentWindows, fakeTmuxWindowCommand{
		session: session,
		window:  window,
		command: append([]string(nil), command...),
	})
	return f.sendKeysErr
}
func (f *fakeTmuxRepository) TypeInWindow(_ context.Context, _, _ string, command []string) error {
	f.typedCommand = append([]string(nil), command...)
	return f.typeInErr
}

type fakeTmuxWindowCommand struct {
	session string
	window  string
	command []string
}

type fakeProviderRepository struct {
	isAvailableErr error
	proposeErr     error
	proposedName   string
	launchCommand  []string
}

func (f *fakeProviderRepository) ProposeTaskName(context.Context, string) (string, error) {
	if f.proposeErr != nil {
		return "", f.proposeErr
	}

	return f.proposedName, nil
}
func (f *fakeProviderRepository) BuildLaunchCommand(task *Task) ([]string, error) {
	if len(f.launchCommand) > 0 {
		return append([]string(nil), f.launchCommand...), nil
	}

	return []string{"codex", task.Prompt}, nil
}
func (f *fakeProviderRepository) IsAvailable(context.Context) error { return f.isAvailableErr }

type fakeRuntimeMonitor struct {
	snapshot RuntimeSnapshot
	err      error
}

func (f *fakeRuntimeMonitor) Snapshot(context.Context, *Task) (RuntimeSnapshot, error) {
	return f.snapshot, f.err
}

func (*fakeRuntimeMonitor) Close() error { return nil }

type fakeRuntimeStateDetector struct {
	state RuntimeState
}

func (f *fakeRuntimeStateDetector) Detect(RuntimeSnapshot) RuntimeState {
	return f.state
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
	validateErr      error
	seedErr          error
	validateRepoRoot string
	validatePaths    []string
	seedInput        SeedWorkspaceInput
	seededPaths      []string
	seedCalled       bool
	seededBeforeTmux bool
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
