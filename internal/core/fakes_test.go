package core

import (
	"context"
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
	createErr   error
	updateErr   error
	listTasks   []*Task
	createdTask *Task
	updatedTask *Task
}

func (f *fakeTaskRepository) CreateTask(_ context.Context, task *Task) error {
	if f.createErr != nil {
		return f.createErr
	}

	clone := *task
	f.createdTask = &clone
	return nil
}

func (f *fakeTaskRepository) UpdateTask(_ context.Context, task *Task) error {
	if f.updateErr != nil {
		return f.updateErr
	}

	clone := *task
	f.updatedTask = &clone
	return nil
}

func (*fakeTaskRepository) GetTask(context.Context, string) (*Task, error) { return nil, ErrTaskNotFound }
func (f *fakeTaskRepository) ListTasks(context.Context) ([]*Task, error)    { return f.listTasks, nil }
func (*fakeTaskRepository) AppendEvent(context.Context, string, string, string) error {
	return nil
}

type fakeGitRepository struct {
	isAvailableErr    error
	detectRepoErr     error
	createWorktreeErr error
	repoContext       RepoContext
	createWorktreeInput CreateWorktreeInput
}

func (f *fakeGitRepository) IsAvailable(context.Context) error { return f.isAvailableErr }
func (f *fakeGitRepository) DetectRepo(context.Context, string) (RepoContext, error) {
	if f.detectRepoErr != nil {
		return RepoContext{}, f.detectRepoErr
	}

	return f.repoContext, nil
}
func (*fakeGitRepository) BranchExists(context.Context, string, string) (bool, error) { return false, nil }
func (f *fakeGitRepository) CreateWorktree(_ context.Context, input CreateWorktreeInput) error {
	f.createWorktreeInput = input
	return f.createWorktreeErr
}

type fakeTmuxRepository struct {
	isAvailableErr  error
	createSessionErr error
	sendKeysErr      error
	createdSession   CreateSessionInput
	sentCommand      []string
}

func (f *fakeTmuxRepository) IsAvailable(context.Context) error { return f.isAvailableErr }
func (*fakeTmuxRepository) SessionExists(context.Context, string) (bool, error) { return false, nil }
func (f *fakeTmuxRepository) CreateSession(_ context.Context, input CreateSessionInput) error {
	f.createdSession = input
	return f.createSessionErr
}
func (*fakeTmuxRepository) AttachOrSwitch(context.Context, string) error { return nil }
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
