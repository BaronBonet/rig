package core

import (
	"context"
	"os"
	"time"

	"agent/internal/pkg/timeutil"
)

type testServiceHarness struct {
	service   *Service
	taskRepo  *fakeTaskRepository
	gitRepo   *fakeGitRepository
	tmuxRepo  *fakeTmuxRepository
	codexRepo *fakeCodexRepository
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
	codexRepo := &fakeCodexRepository{}

	return &testServiceHarness{
		service: NewService(taskRepo, gitRepo, tmuxRepo, codexRepo, fakeClock{
			now: time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC),
		}, Config{
			DatabasePath: "/tmp/agent/state.db",
		}),
		taskRepo:  taskRepo,
		gitRepo:   gitRepo,
		tmuxRepo:  tmuxRepo,
		codexRepo: codexRepo,
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
	isAvailableErr   error
	createSessionErr error
	killSessionErr   error
	killSessionHook  func()
	sendKeysErr      error
	sessionExists    bool
	attachedSession  string
	createdSession   CreateSessionInput
	sentCommand      []string
	killedSessions   []string
}

func (f *fakeTmuxRepository) IsAvailable(context.Context) error { return f.isAvailableErr }
func (f *fakeTmuxRepository) SessionExists(context.Context, string) (bool, error) {
	return f.sessionExists, nil
}
func (f *fakeTmuxRepository) CreateSession(_ context.Context, input CreateSessionInput) error {
	f.createdSession = input
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
	return nil
}

func (f *fakeTmuxRepository) AttachOrSwitch(_ context.Context, session string) error {
	f.attachedSession = session
	return nil
}
func (f *fakeTmuxRepository) SendKeys(_ context.Context, _ string, command []string) error {
	f.sentCommand = append([]string(nil), command...)
	return f.sendKeysErr
}

type fakeCodexRepository struct {
	isAvailableErr error
	proposeErr     error
	proposedName   string
	launchCommand  []string
}

func (f *fakeCodexRepository) ProposeTaskName(context.Context, string) (string, error) {
	if f.proposeErr != nil {
		return "", f.proposeErr
	}

	return f.proposedName, nil
}
func (f *fakeCodexRepository) BuildLaunchCommand(task *Task) ([]string, error) {
	if len(f.launchCommand) > 0 {
		return append([]string(nil), f.launchCommand...), nil
	}

	return []string{"codex", task.Prompt}, nil
}
func (f *fakeCodexRepository) IsAvailable(context.Context) error { return f.isAvailableErr }

type fakeClock struct {
	now time.Time
}

func (f fakeClock) Now() time.Time { return f.now }

var _ timeutil.Clock = fakeClock{}
