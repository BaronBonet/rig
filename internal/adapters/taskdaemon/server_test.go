package taskdaemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// fakeTaskService is a stateful in-memory core.TaskService used to exercise
// the socket transport end to end: real frontend client, real wire, real
// dispatcher, fake behaviour behind the port.
type fakeTaskService struct {
	mu sync.Mutex

	tasks      []*core.Task
	activity   map[string][]core.TaskActivityEvent
	usage      map[string]*core.TaskUsage
	prs        map[string][]core.RepoPullRequest
	taskPRs    map[string]core.TaskPullRequests
	storedPRs  map[string]core.TaskPullRequests
	setup      *core.ProviderSetup
	detections []core.ProviderDetection
	models     map[core.Provider][]core.ProviderModel
	// launchDefaults are the saved Launch defaults, which SaveLaunchDefault
	// sets and an empty choice deletes.
	launchDefaults map[core.Provider]core.LaunchChoice
	latest         map[string]*core.TaskStatusUpdate
	taskAtPath     map[string]*core.Task
	repoDirs       []string
	repos          []core.KnownRepo
	// repoDirSuggestions is the repo directory suggested for each working
	// directory.
	repoDirSuggestions map[string]string

	createSteps []core.TaskCreateProgressStep
	createTask  *core.Task
	createErr   error
	// createInputs are the inputs CreateTaskStream received.
	createInputs []core.CreateTaskInput

	updates      chan core.TaskStatusUpdate
	subscribeErr error
	subscribeCtx chan context.Context

	deleted     []string
	reconnected []string
	// removedAgents are the task and agent session IDs RemoveAgentSession
	// received, each as "task/agent".
	removedAgents []string

	errByOp map[string]error
}

func newFakeTaskService() *fakeTaskService {
	return &fakeTaskService{
		activity:       map[string][]core.TaskActivityEvent{},
		usage:          map[string]*core.TaskUsage{},
		prs:            map[string][]core.RepoPullRequest{},
		taskPRs:        map[string]core.TaskPullRequests{},
		storedPRs:      map[string]core.TaskPullRequests{},
		models:         map[core.Provider][]core.ProviderModel{},
		launchDefaults: map[core.Provider]core.LaunchChoice{},
		latest:         map[string]*core.TaskStatusUpdate{},
		taskAtPath:     map[string]*core.Task{},
		updates:        make(chan core.TaskStatusUpdate, 8),
		subscribeCtx:   make(chan context.Context, 1),
		errByOp:        map[string]error{},
	}
}

func (f *fakeTaskService) taskCreateResult() (<-chan core.TaskCreateEvent, error) {
	f.mu.Lock()
	steps, task, err := f.createSteps, f.createTask, f.createErr
	f.mu.Unlock()

	events := make(chan core.TaskCreateEvent, len(steps)+1)
	for _, step := range steps {
		progress := &core.TaskCreateProgressEvent{Step: step}
		// As in core, the task exists once its name is suggested.
		if step != core.TaskCreateProgressSuggestingName {
			progress.Task = task
		}
		events <- core.TaskCreateEvent{Progress: progress}
	}
	if err != nil {
		events <- core.TaskCreateEvent{Err: err, Task: task}
	} else {
		events <- core.TaskCreateEvent{Task: task}
	}
	close(events)

	return events, nil
}

func (f *fakeTaskService) CreateTaskStream(
	_ context.Context,
	input core.CreateTaskInput,
) (<-chan core.TaskCreateEvent, error) {
	f.mu.Lock()
	f.tasks = append(f.tasks, f.createTask)
	f.createInputs = append(f.createInputs, input)
	f.mu.Unlock()

	return f.taskCreateResult()
}

func (f *fakeTaskService) RetryTaskCreationStream(
	_ context.Context,
	taskID string,
) (<-chan core.TaskCreateEvent, error) {
	return f.taskCreateResult()
}

func (f *fakeTaskService) ListRepoPullRequests(_ context.Context, cwd string) ([]core.RepoPullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prs[cwd], f.errByOp["list_repo_pull_requests"]
}

func (f *fakeTaskService) TaskPullRequests(_ context.Context, taskID string) (core.TaskPullRequests, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.taskPRs[taskID], f.errByOp["task_pull_requests"]
}

func (f *fakeTaskService) StoredTaskPullRequests(_ context.Context, taskID string) (core.TaskPullRequests, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.storedPRs[taskID], f.errByOp["stored_task_pull_requests"]
}

func (f *fakeTaskService) GetTaskActivity(
	_ context.Context,
	taskID string,
	limit int,
) ([]core.TaskActivityEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	events := f.activity[taskID]
	if limit > 0 && limit < len(events) {
		events = events[len(events)-limit:]
	}
	return events, f.errByOp["get_task_activity"]
}

func (f *fakeTaskService) GetTaskUsage(_ context.Context, taskID string) (*core.TaskUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.usage[taskID], f.errByOp["get_task_usage"]
}

func (f *fakeTaskService) ListTasks(context.Context) ([]*core.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tasks, f.errByOp["list_tasks"]
}

func (f *fakeTaskService) TaskAtPath(_ context.Context, path string) (*core.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.taskAtPath[path], f.errByOp["task_at_path"]
}

func (f *fakeTaskService) LatestTaskStatus(_ context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latest[taskID], f.errByOp["latest_task_status"]
}

func (f *fakeTaskService) SubscribeTaskStatus(
	ctx context.Context,
	taskID string,
) (<-chan core.TaskStatusUpdate, error) {
	f.mu.Lock()
	err := f.subscribeErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case f.subscribeCtx <- ctx:
	default:
	}

	return f.updates, nil
}

func (f *fakeTaskService) DeleteTask(_ context.Context, taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, taskID)
	return f.errByOp["delete_task"]
}

func (f *fakeTaskService) RemoveAgentSession(_ context.Context, taskID string, agentSessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removedAgents = append(f.removedAgents, taskID+"/"+agentSessionID)
	return f.errByOp["remove_agent_session"]
}

func (f *fakeTaskService) ReconnectTaskSession(_ context.Context, taskID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconnected = append(f.reconnected, taskID)
	return f.errByOp["reconnect_task_session"]
}

func (f *fakeTaskService) GetProviderSetup(context.Context) (*core.ProviderSetup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setup, f.errByOp["get_provider_setup"]
}

func (f *fakeTaskService) SaveProviderSetup(_ context.Context, setup core.ProviderSetup) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errByOp["save_provider_setup"]; err != nil {
		return err
	}
	f.setup = &setup
	return nil
}

func (f *fakeTaskService) DetectProviders(context.Context) ([]core.ProviderDetection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.detections, f.errByOp["detect_providers"]
}

func (f *fakeTaskService) ListProviderModels(
	_ context.Context,
	provider core.Provider,
) ([]core.ProviderModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.models[provider], f.errByOp["list_provider_models"]
}

func (f *fakeTaskService) GetLaunchDefaults(context.Context) (map[core.Provider]core.LaunchChoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.launchDefaults), f.errByOp["get_launch_defaults"]
}

func (f *fakeTaskService) SaveLaunchDefault(
	_ context.Context,
	provider core.Provider,
	choice core.LaunchChoice,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errByOp["save_launch_default"]; err != nil {
		return err
	}
	if choice.IsZero() {
		delete(f.launchDefaults, provider)
	} else {
		f.launchDefaults[provider] = choice
	}
	return nil
}

func (f *fakeTaskService) GetRepoDirs(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repoDirs, f.errByOp["get_repo_dirs"]
}

func (f *fakeTaskService) SaveRepoDirs(_ context.Context, dirs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errByOp["save_repo_dirs"]; err != nil {
		return err
	}
	f.repoDirs = dirs
	return nil
}

func (f *fakeTaskService) ListRepos(context.Context) ([]core.KnownRepo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repos, f.errByOp["list_repos"]
}

func (f *fakeTaskService) SuggestRepoDir(_ context.Context, cwd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repoDirSuggestions[cwd], f.errByOp["suggest_repo_dir"]
}

// startTestFrontend serves a fake TaskService on a real Unix socket and
// returns the real daemon-backed frontend client pointed at it, so tests
// cross the seam exactly the way the TUI does.
func startTestFrontend(t *testing.T, svc core.TaskService) *frontend {
	t.Helper()

	socketPath := serverTestSocketPath(t)
	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errCh)
	})

	return &frontend{socketPath: socketPath}
}

func TestUnaryOperationsRoundTrip(t *testing.T) {
	t.Parallel()

	svc := newFakeTaskService()
	svc.tasks = []*core.Task{
		{ID: "task-1", DisplayName: "add retries", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "fix flake", Provider: core.ProviderClaude, ParentTaskID: "task-1"},
	}
	svc.taskAtPath["/tmp/repo_fix-flake/internal"] = svc.tasks[1]
	svc.activity["task-1"] = []core.TaskActivityEvent{
		{TaskID: "task-1", Role: core.TaskActivityRoleUser, Text: "add retries"},
		{TaskID: "task-1", Role: core.TaskActivityRoleAssistant, Text: "editing main.go"},
	}
	svc.usage["task-1"] = &core.TaskUsage{
		Tokens:           &core.TaskTokenUsage{InputTokens: 100, OutputTokens: 25},
		AgentCompactions: map[string]core.Compactions{"agent-1": {Total: 3, Auto: 2, Manual: 1}},
	}
	svc.prs["/tmp/repo"] = []core.RepoPullRequest{{Number: 7, Title: "retry flow", BranchName: "retries"}}
	svc.taskPRs["task-1"] = core.TaskPullRequests{
		PullRequests: []core.PullRequest{
			{Number: 5, State: core.PRStateMerged, HeadBranch: "retries", HeadCommit: "5aa"},
			{Number: 7, State: core.PRStateOpen, HeadBranch: "retries-2", HeadCommit: "7bb"},
		},
		UnshippedWork: core.UnshippedWork{Commits: 2, UncommittedChanges: true},
	}
	svc.storedPRs["task-1"] = core.TaskPullRequests{
		PullRequests: []core.PullRequest{
			{Number: 5, State: core.PRStateOpen, HeadBranch: "retries", HeadCommit: "5aa"},
		},
	}
	svc.setup = &core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex},
		Default:    core.ProviderCodex,
	}
	svc.detections = []core.ProviderDetection{{Provider: core.ProviderCodex, Ready: true}}
	svc.models[core.ProviderCodex] = []core.ProviderModel{
		{ID: "gpt-5.5", DisplayName: "GPT-5.5", ReasoningLevels: []string{"low", "medium", "high"}},
		{ID: "gpt-5.5-mini", DisplayName: "GPT-5.5 Mini"},
	}
	svc.repoDirs = []string{"/work"}
	svc.repos = []core.KnownRepo{
		{Name: "backend", Root: "/work/backend", TaskCount: 2},
		{Name: "frontend", Root: "/work/frontend"},
	}
	svc.repoDirSuggestions = map[string]string{"/work/backend/internal": "/work"}
	svc.latest["task-1"] = &core.TaskStatusUpdate{
		TaskID:             "task-1",
		Provider:           core.ProviderCodex,
		Phase:              core.TaskStatusPhaseWorking,
		LeadAgentSessionID: "agent-1",
		AgentSessions: []core.LiveAgentSession{{
			ID:       "agent-1",
			Provider: core.ProviderCodex,
			Pane:     core.TmuxPaneRef{Server: statusTestServer, ID: "%4"},
			Status:   core.AgentSessionStatus{Phase: core.TaskStatusPhaseWorking},
		}},
	}

	client := startTestFrontend(t, svc)
	ctx := context.Background()

	t.Run("list tasks", func(t *testing.T) {
		tasks, err := client.ListTasks(ctx)
		require.NoError(t, err)
		require.Len(t, tasks, 2)
		require.Equal(t, "task-1", tasks[0].ID)
		require.Equal(t, "task-1", tasks[1].ParentTaskID)
	})

	t.Run("task at path", func(t *testing.T) {
		task, err := client.TaskAtPath(ctx, "/tmp/repo_fix-flake/internal")
		require.NoError(t, err)
		require.Equal(t, svc.tasks[1], task)
	})

	t.Run("task at path returns nil when no task's worktree contains it", func(t *testing.T) {
		task, err := client.TaskAtPath(ctx, "/tmp/repo")
		require.NoError(t, err)
		require.Nil(t, task)
	})

	t.Run("get task activity honors limit", func(t *testing.T) {
		events, err := client.GetTaskActivity(ctx, "task-1", 1)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, "editing main.go", events[0].Text)
	})

	t.Run("get task activity requires task id", func(t *testing.T) {
		_, err := client.GetTaskActivity(ctx, "  ", 5)
		require.ErrorContains(t, err, "task_id required")
	})

	t.Run("get task usage", func(t *testing.T) {
		usage, err := client.GetTaskUsage(ctx, "task-1")
		require.NoError(t, err)
		require.Equal(t, svc.usage["task-1"], usage)
	})

	t.Run("get task usage requires task id", func(t *testing.T) {
		_, err := client.GetTaskUsage(ctx, "")
		require.ErrorContains(t, err, "task_id required")
	})

	t.Run("list repo pull requests", func(t *testing.T) {
		prs, err := client.ListRepoPullRequests(ctx, "/tmp/repo")
		require.NoError(t, err)
		require.Len(t, prs, 1)
		require.Equal(t, 7, prs[0].Number)
	})

	t.Run("task pull requests", func(t *testing.T) {
		pullRequests, err := client.TaskPullRequests(ctx, "task-1")
		require.NoError(t, err)
		require.Equal(t, svc.taskPRs["task-1"], pullRequests)
	})

	t.Run("task pull requests of a task without any", func(t *testing.T) {
		pullRequests, err := client.TaskPullRequests(ctx, "task-2")
		require.NoError(t, err)
		require.Equal(t, core.TaskPullRequests{}, pullRequests)
	})

	t.Run("task pull requests requires task id", func(t *testing.T) {
		_, err := client.TaskPullRequests(ctx, " ")
		require.ErrorContains(t, err, "task_id required")
	})

	t.Run("stored task pull requests", func(t *testing.T) {
		pullRequests, err := client.StoredTaskPullRequests(ctx, "task-1")
		require.NoError(t, err)
		require.Equal(t, svc.storedPRs["task-1"], pullRequests)
		_, err = client.StoredTaskPullRequests(ctx, " ")
		require.ErrorContains(t, err, "task_id required")
	})

	t.Run("latest task status", func(t *testing.T) {
		update, err := client.LatestTaskStatus(ctx, "task-1")
		require.NoError(t, err)
		require.Equal(t, svc.latest["task-1"], update)
	})

	t.Run("latest task status returns nil update when none published", func(t *testing.T) {
		update, err := client.LatestTaskStatus(ctx, "task-2")
		require.NoError(t, err)
		require.Nil(t, update)
	})

	t.Run("get provider setup", func(t *testing.T) {
		setup, err := client.GetProviderSetup(ctx)
		require.NoError(t, err)
		require.Equal(t, core.ProviderCodex, setup.Default)
	})

	t.Run("save provider setup", func(t *testing.T) {
		next := core.ProviderSetup{
			Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
			Default:    core.ProviderClaude,
		}
		require.NoError(t, client.SaveProviderSetup(ctx, next))
		saved, err := client.GetProviderSetup(ctx)
		require.NoError(t, err)
		require.Equal(t, core.ProviderClaude, saved.Default)
	})

	t.Run("detect providers", func(t *testing.T) {
		detections, err := client.DetectProviders(ctx)
		require.NoError(t, err)
		require.Len(t, detections, 1)
		require.True(t, detections[0].Ready)
	})

	t.Run("list provider models", func(t *testing.T) {
		models, err := client.ListProviderModels(ctx, core.ProviderCodex)
		require.NoError(t, err)
		require.Equal(t, svc.models[core.ProviderCodex], models)
	})

	t.Run("list provider models trims the provider", func(t *testing.T) {
		models, err := client.ListProviderModels(ctx, "  codex  ")
		require.NoError(t, err)
		require.Equal(t, svc.models[core.ProviderCodex], models)
	})

	t.Run("list provider models requires provider", func(t *testing.T) {
		_, err := client.ListProviderModels(ctx, " ")
		require.ErrorContains(t, err, "list_provider_models provider required")
	})

	t.Run("launch defaults", func(t *testing.T) {
		defaults, err := client.GetLaunchDefaults(ctx)
		require.NoError(t, err)
		require.Empty(t, defaults)

		choice := core.LaunchChoice{Model: "opus[1m]", ReasoningLevel: "high"}
		require.NoError(t, client.SaveLaunchDefault(ctx, core.ProviderClaude, choice))
		require.NoError(t, client.SaveLaunchDefault(ctx, "  codex  ", core.LaunchChoice{ReasoningLevel: "low"}))
		defaults, err = client.GetLaunchDefaults(ctx)
		require.NoError(t, err)
		require.Equal(t, map[core.Provider]core.LaunchChoice{
			core.ProviderClaude: choice,
			core.ProviderCodex:  {ReasoningLevel: "low"},
		}, defaults)

		require.NoError(t, client.SaveLaunchDefault(ctx, core.ProviderClaude, core.LaunchChoice{}))
		defaults, err = client.GetLaunchDefaults(ctx)
		require.NoError(t, err)
		require.Equal(t, map[core.Provider]core.LaunchChoice{core.ProviderCodex: {ReasoningLevel: "low"}}, defaults)
	})

	t.Run("save launch default requires provider", func(t *testing.T) {
		err := client.SaveLaunchDefault(ctx, " ", core.LaunchChoice{Model: "gpt-5.5"})
		require.ErrorContains(t, err, "save_launch_default provider required")
	})

	t.Run("launch default service errors surface as client errors", func(t *testing.T) {
		svc.mu.Lock()
		svc.errByOp["get_launch_defaults"] = errors.New("config unreadable")
		svc.errByOp["save_launch_default"] = errors.New(`model "-x" must not start with a dash`)
		svc.mu.Unlock()
		t.Cleanup(func() {
			svc.mu.Lock()
			delete(svc.errByOp, "get_launch_defaults")
			delete(svc.errByOp, "save_launch_default")
			svc.mu.Unlock()
		})

		_, err := client.GetLaunchDefaults(ctx)
		require.ErrorContains(t, err, "config unreadable")
		err = client.SaveLaunchDefault(ctx, core.ProviderCodex, core.LaunchChoice{Model: "-x"})
		require.ErrorContains(t, err, `model "-x" must not start with a dash`)
	})

	t.Run("get repo dirs", func(t *testing.T) {
		dirs, err := client.GetRepoDirs(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/work"}, dirs)
	})

	t.Run("save repo dirs", func(t *testing.T) {
		require.NoError(t, client.SaveRepoDirs(ctx, []string{"~/code", "/work"}))
		dirs, err := client.GetRepoDirs(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"~/code", "/work"}, dirs)
	})

	t.Run("save no repo dirs", func(t *testing.T) {
		require.NoError(t, client.SaveRepoDirs(ctx, nil))
		dirs, err := client.GetRepoDirs(ctx)
		require.NoError(t, err)
		require.Empty(t, dirs)
	})

	t.Run("list repos", func(t *testing.T) {
		repos, err := client.ListRepos(ctx)
		require.NoError(t, err)
		require.Equal(t, svc.repos, repos)
	})

	t.Run("suggest repo dir", func(t *testing.T) {
		suggestion, err := client.SuggestRepoDir(ctx, "/work/backend/internal")
		require.NoError(t, err)
		require.Equal(t, "/work", suggestion)
	})

	t.Run("suggest no repo dir", func(t *testing.T) {
		suggestion, err := client.SuggestRepoDir(ctx, "/tmp")
		require.NoError(t, err)
		require.Empty(t, suggestion)
	})

	t.Run("reconnect task session", func(t *testing.T) {
		require.NoError(t, client.ReconnectTaskSession(ctx, "task-1"))
		svc.mu.Lock()
		defer svc.mu.Unlock()
		require.Equal(t, []string{"task-1"}, svc.reconnected)
	})

	t.Run("delete task", func(t *testing.T) {
		require.NoError(t, client.DeleteTask(ctx, "task-2"))
		svc.mu.Lock()
		defer svc.mu.Unlock()
		require.Equal(t, []string{"task-2"}, svc.deleted)
	})

	t.Run("delete task requires task id", func(t *testing.T) {
		require.ErrorContains(t, client.DeleteTask(ctx, ""), "task_id required")
	})

	t.Run("remove agent session", func(t *testing.T) {
		require.NoError(t, client.RemoveAgentSession(ctx, " task-1 ", " agent-2 "))
		svc.mu.Lock()
		defer svc.mu.Unlock()
		require.Equal(t, []string{"task-1/agent-2"}, svc.removedAgents)
	})

	t.Run("remove agent session requires task and agent session ids", func(t *testing.T) {
		require.ErrorContains(t, client.RemoveAgentSession(ctx, "", "agent-2"), "task_id required")
		require.ErrorContains(t, client.RemoveAgentSession(ctx, "task-1", " "), "agent_session_id required")
	})

	t.Run("remove agent session errors surface as client errors", func(t *testing.T) {
		svc.mu.Lock()
		svc.errByOp["remove_agent_session"] = errors.New("close agent pane: tmux offline")
		svc.mu.Unlock()
		require.ErrorContains(t, client.RemoveAgentSession(ctx, "task-1", "agent-3"), "tmux offline")
		svc.mu.Lock()
		delete(svc.errByOp, "remove_agent_session")
		svc.mu.Unlock()
	})

	t.Run("service errors surface as client errors", func(t *testing.T) {
		svc.mu.Lock()
		svc.errByOp["list_tasks"] = errors.New("repository offline")
		svc.mu.Unlock()
		_, err := client.ListTasks(ctx)
		require.ErrorContains(t, err, "repository offline")
		svc.mu.Lock()
		delete(svc.errByOp, "list_tasks")
		svc.mu.Unlock()
	})

	t.Run("repo errors surface as client errors", func(t *testing.T) {
		svc.mu.Lock()
		svc.errByOp["list_repos"] = errors.New("repository offline")
		svc.errByOp["save_repo_dirs"] = errors.New(`repo directory "code" must be an absolute path`)
		svc.errByOp["suggest_repo_dir"] = errors.New("git offline")
		svc.mu.Unlock()
		_, err := client.ListRepos(ctx)
		require.ErrorContains(t, err, "repository offline")
		_, err = client.SuggestRepoDir(ctx, "/work/backend")
		require.ErrorContains(t, err, "git offline")
		require.ErrorContains(
			t,
			client.SaveRepoDirs(ctx, []string{"code"}),
			`repo directory "code" must be an absolute path`,
		)
		svc.mu.Lock()
		delete(svc.errByOp, "list_repos")
		delete(svc.errByOp, "save_repo_dirs")
		delete(svc.errByOp, "suggest_repo_dir")
		svc.mu.Unlock()
	})

	t.Run("task at path errors surface as client errors", func(t *testing.T) {
		svc.mu.Lock()
		svc.errByOp["task_at_path"] = errors.New("repository offline")
		svc.mu.Unlock()
		_, err := client.TaskAtPath(ctx, "/tmp/repo_fix-flake")
		require.ErrorContains(t, err, "repository offline")
		svc.mu.Lock()
		delete(svc.errByOp, "task_at_path")
		svc.mu.Unlock()
	})

	// The TUI keeps a task's pull requests when a refresh fails, so the
	// failure must reach it rather than an empty list.
	t.Run("task pull request errors surface as client errors", func(t *testing.T) {
		svc.mu.Lock()
		svc.errByOp["task_pull_requests"] = errors.New("gh: not logged in")
		svc.mu.Unlock()
		_, err := client.TaskPullRequests(ctx, "task-1")
		require.ErrorContains(t, err, "gh: not logged in")
		svc.mu.Lock()
		delete(svc.errByOp, "task_pull_requests")
		svc.mu.Unlock()
	})
}

func TestCreateTaskStreamRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("streams progress before terminal task", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createSteps = []core.TaskCreateProgressStep{
			core.TaskCreateProgressSuggestingName,
			core.TaskCreateProgressCreatingWorktree,
		}
		svc.createTask = &core.Task{ID: "task-1", DisplayName: "add retries"}
		client := startTestFrontend(t, svc)

		events, err := client.CreateTaskStream(context.Background(), core.CreateTaskInput{
			Cwd:      "/tmp/repo",
			Prompt:   "add retries",
			Provider: core.ProviderCodex,
		})
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		// A progress event's task rides along without ending the stream.
		require.Len(t, got, 3)
		require.Equal(t, core.TaskCreateProgressSuggestingName, got[0].Progress.Step)
		require.Nil(t, got[0].Progress.Task)
		require.Equal(t, core.TaskCreateProgressCreatingWorktree, got[1].Progress.Step)
		require.Nil(t, got[1].Task)
		require.NotNil(t, got[1].Progress.Task)
		require.Equal(t, "add retries", got[1].Progress.Task.DisplayName)
		require.NotNil(t, got[2].Task)
		require.Equal(t, "task-1", got[2].Task.ID)
	})

	t.Run("carries the launch choice to the daemon and back", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createTask = &core.Task{
			ID:             "task-1",
			Provider:       core.ProviderClaude,
			Model:          "opus[1m]",
			ReasoningLevel: "high",
		}
		client := startTestFrontend(t, svc)

		input := core.CreateTaskInput{
			Cwd:                  "/tmp/repo",
			Prompt:               "add retries",
			Provider:             core.ProviderClaude,
			Model:                "opus[1m]",
			ReasoningLevel:       "high",
			RememberLaunchChoice: true,
		}
		events, err := client.CreateTaskStream(context.Background(), input)
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		require.Len(t, got, 1)
		require.Equal(t, svc.createTask, got[0].Task)
		svc.mu.Lock()
		defer svc.mu.Unlock()
		require.Equal(t, []core.CreateTaskInput{input}, svc.createInputs)
	})

	t.Run("sends the whole input", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createTask = &core.Task{ID: "task-1"}
		client := startTestFrontend(t, svc)
		input := core.CreateTaskInput{
			Source: core.CreateTaskSource{PullRequest: &core.RepoPullRequest{
				Number:     7,
				Title:      "retry flow",
				BranchName: "retries",
				State:      core.PRStateOpen,
			}},
			Cwd:               "/tmp/backend",
			Prompt:            "call the new endpoint",
			Provider:          core.ProviderClaude,
			TargetRepo:        "../frontend",
			SubmitPrompt:      true,
			LinkToCallingTask: true,
		}

		events, err := client.CreateTaskStream(context.Background(), input)
		require.NoError(t, err)
		for range events {
		}

		svc.mu.Lock()
		defer svc.mu.Unlock()
		require.Equal(t, []core.CreateTaskInput{input}, svc.createInputs)
	})

	t.Run("terminal error carries partial task for retry", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createSteps = []core.TaskCreateProgressStep{core.TaskCreateProgressSuggestingName}
		svc.createTask = &core.Task{ID: "task-1", CreationStatus: core.TaskCreationStatusFailed}
		svc.createErr = errors.New("worktree creation failed")
		client := startTestFrontend(t, svc)

		events, err := client.CreateTaskStream(context.Background(), core.CreateTaskInput{
			Cwd:      "/tmp/repo",
			Prompt:   "add retries",
			Provider: core.ProviderCodex,
		})
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		require.Len(t, got, 2)
		terminal := got[1]
		require.ErrorContains(t, terminal.Err, "worktree creation failed")
		require.NotNil(t, terminal.Task)
		require.Equal(t, "task-1", terminal.Task.ID)
	})

	t.Run("retry task creation streams the same shape", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.createSteps = []core.TaskCreateProgressStep{core.TaskCreateProgressStartingSession}
		svc.createTask = &core.Task{ID: "task-1"}
		client := startTestFrontend(t, svc)

		events, err := client.RetryTaskCreationStream(context.Background(), "task-1")
		require.NoError(t, err)

		var got []core.TaskCreateEvent
		for event := range events {
			got = append(got, event)
		}
		require.Len(t, got, 2)
		require.Equal(t, core.TaskCreateProgressStartingSession, got[0].Progress.Step)
		require.Equal(t, "task-1", got[1].Task.ID)
	})

	t.Run("create task requires input payload", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		conn, err := dialDaemonSocket(context.Background(), client.socketPath)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, json.NewEncoder(conn).Encode(socketRequest{Command: socketCommandCreateTask}))

		var resp socketEnvelope
		require.NoError(t, json.NewDecoder(conn).Decode(&resp))
		require.Equal(t, socketEnvelopeError, resp.Type)
		require.Contains(t, resp.Error, "input required")
	})
}

// statusTestServer is the tmux server the status round-trip tests' agents
// run on.
var statusTestServer = core.TmuxServer{SocketPath: "/tmp/tmux-501/default", PID: 42}

func TestSubscribeTaskStatusRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("streams updates after ack", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stream, err := client.SubscribeTaskStatus(ctx, "task-1")
		require.NoError(t, err)

		observedAt := time.Now().UTC()
		expected := core.TaskStatusUpdate{
			TaskID:             "task-1",
			Provider:           core.ProviderCodex,
			Phase:              core.TaskStatusPhaseWorking,
			RawEventName:       "PreToolUse",
			ObservedAt:         observedAt,
			Unseen:             true,
			LeadAgentSessionID: "agent-2",
			AgentSessions: []core.LiveAgentSession{
				{
					ID:       "agent-1",
					Provider: core.ProviderClaude,
					Pane:     core.TmuxPaneRef{Server: statusTestServer, ID: "%3"},
					Location: &core.TmuxPaneLocation{WindowName: "task"},
					Status: core.AgentSessionStatus{
						Phase:          core.TaskStatusPhaseWaitingForInput,
						RawEventName:   "Stop",
						ObservedAt:     observedAt.Add(-time.Minute),
						BackgroundWork: core.TaskBackgroundWork{Subagents: 2},
					},
					LatestPrompt: "add retries",
					Unseen:       true,
				},
				{
					ID:       "agent-2",
					Provider: core.ProviderCodex,
					Pane:     core.TmuxPaneRef{Server: statusTestServer, ID: "%7"},
					Location: &core.TmuxPaneLocation{WindowName: "codex", WindowIndex: 1, PaneIndex: 1},
					Status: core.AgentSessionStatus{
						Phase:        core.TaskStatusPhaseWorking,
						RawEventName: "PreToolUse",
						ObservedAt:   observedAt,
					},
				},
			},
		}
		svc.updates <- expected

		select {
		case got := <-stream:
			require.Equal(t, expected, got)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for streamed task status update")
		}
	})

	t.Run("subscribe failure surfaces as error", func(t *testing.T) {
		svc := newFakeTaskService()
		svc.subscribeErr = errors.New("subscribe failed")
		client := startTestFrontend(t, svc)

		_, err := client.SubscribeTaskStatus(context.Background(), "task-1")
		require.ErrorContains(t, err, "subscribe failed")
	})

	t.Run("requires task id", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		_, err := client.SubscribeTaskStatus(context.Background(), "  ")
		require.ErrorContains(t, err, "task_id required")
	})

	t.Run("client cancellation cancels the backend subscription", func(t *testing.T) {
		svc := newFakeTaskService()
		client := startTestFrontend(t, svc)

		ctx, cancel := context.WithCancel(context.Background())
		_, err := client.SubscribeTaskStatus(ctx, "task-1")
		require.NoError(t, err)

		var backendCtx context.Context
		select {
		case backendCtx = <-svc.subscribeCtx:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for backend subscription")
		}

		cancel()

		select {
		case <-backendCtx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for backend subscribe context cancellation")
		}
	})
}

func TestHandshakeProbes(t *testing.T) {
	t.Parallel()

	client := startTestFrontend(t, newFakeTaskService())
	ctx := context.Background()

	require.NoError(t, probeSocketHealth(ctx, client.socketPath))
	require.NoError(t, probeFrontendProtocol(ctx, client.socketPath))
	require.NoError(t, probeFrontendBuildVersion(ctx, client.socketPath))
}

// TestHandshakeDetectsLegacyDaemon simulates a daemon still speaking the
// protocol-8 fat-envelope wire and asserts the probes report the mismatch
// that triggers the auto-restart. This is the one cross-version contract the
// wire must keep. See docs/adr/0002-version-locked-socket-protocol.md.
func TestHandshakeDetectsLegacyDaemon(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	listener, err := listenUnixSocket(context.Background(), socketPath)
	require.NoError(t, err)
	defer listener.Close()

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var req socketRequest
				if decodeErr := json.NewDecoder(conn).Decode(&req); decodeErr != nil {
					return
				}
				// Legacy protocol-8 daemons answered with top-level fields.
				switch req.Command {
				case socketCommandHealth:
					_, _ = conn.Write([]byte(`{"type":"health","ok":true}` + "\n"))
				case socketCommandProtocolVersion:
					_, _ = conn.Write([]byte(`{"type":"protocol_version","ok":true,"protocol_version":8}` + "\n"))
				case socketCommandFrontendBuildVersion:
					_, _ = conn.Write(
						[]byte(`{"type":"frontend_build_version","ok":true,"version":"some-old-build"}` + "\n"),
					)
				}
			}(conn)
		}
	}()

	ctx := context.Background()
	require.NoError(t, probeSocketHealth(ctx, socketPath))
	require.ErrorContains(t, probeFrontendProtocol(ctx, socketPath), "protocol version mismatch")
	require.ErrorContains(t, probeFrontendBuildVersion(ctx, socketPath), "build version mismatch")
}

func TestUnixSocketServer_RejectsUnsupportedCommand(t *testing.T) {
	t.Parallel()

	client := startTestFrontend(t, newFakeTaskService())

	// Rig no longer starts agent sessions itself, so the daemon stopped
	// serving that operation.
	for _, command := range []string{"no_such_command", "start_agent_session"} {
		t.Run(command, func(t *testing.T) {
			conn, err := dialDaemonSocket(context.Background(), client.socketPath)
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, json.NewEncoder(conn).Encode(socketRequest{Command: command}))

			var resp socketEnvelope
			require.NoError(t, json.NewDecoder(conn).Decode(&resp))
			require.Equal(t, socketEnvelopeError, resp.Type)
			require.Contains(t, resp.Error, "unsupported command")
		})
	}
}

func TestUnixSocketServer_SecuresSocketDirectoryAndSocketPermissions(t *testing.T) {
	t.Parallel()

	socketDir := serverTestSocketDir(t)
	require.NoError(t, os.Chmod(socketDir, 0o755))
	socketPath := filepath.Join(socketDir, "daemon.sock")

	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, newFakeTaskService(), nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	socketDirInfo, err := os.Stat(socketDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), socketDirInfo.Mode().Perm())

	socketInfo, err := os.Stat(socketPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), socketInfo.Mode().Perm())

	cancel()
	require.NoError(t, <-errCh)
}

func TestAuthorizeUnixSocketPeerUIDAllowsOnlyMatchingUID(t *testing.T) {
	t.Parallel()

	require.NoError(t, authorizeUnixSocketPeerUID(501, 501))
	require.Error(t, authorizeUnixSocketPeerUID(502, 501))
}

func TestHTTPHookServer_DelegatesToInjectedHookHandler(t *testing.T) {
	t.Parallel()

	called := false
	server := newHTTPHookServer([]core.TaskDaemonHookRoute{{
		Path: "/hook",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusAccepted)
		}),
	}})

	req := httptestNewRequest(t, http.MethodPost, "/hook", map[string]any{"ok": true})

	rec := newRecorder()
	server.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	require.True(t, called)
}

func TestListenForHTTPHooksRejectsNonLoopbackAddress(t *testing.T) {
	t.Parallel()

	listener, err := listenForHTTPHooks(t.Context(), "0.0.0.0:0")
	if listener != nil {
		require.NoError(t, listener.Close())
	}
	require.Error(t, err)
}

func waitForUnixSocketServer(t *testing.T, socketPath string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := probeSocketHealth(context.Background(), socketPath); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("unix socket server at %s did not become healthy", socketPath)
}

func serverTestSocketPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(serverTestSocketDir(t), "daemon.sock")
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	return path
}

func serverTestSocketDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp(os.TempDir(), "rig-td-")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	return dir
}

type recorder struct {
	HeaderMap http.Header
	Body      bytes.Buffer
	Code      int
}

func newRecorder() *recorder {
	return &recorder{HeaderMap: make(http.Header)}
}

func (r *recorder) Header() http.Header {
	return r.HeaderMap
}

func (r *recorder) Write(data []byte) (int, error) {
	if r.Code == 0 {
		r.Code = http.StatusOK
	}
	return r.Body.Write(data)
}

func (r *recorder) WriteHeader(statusCode int) {
	r.Code = statusCode
}

func httptestNewRequest(t *testing.T, method string, target string, payload map[string]any) *http.Request {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), method, target, bytes.NewReader(body))
	require.NoError(t, err)
	return req
}

// outlivingCreateService's creation sends its events into a buffer, as core's
// does: its first step, then, once resumed, its next step, which the daemon
// cannot pass on to a frontend that is gone. It gives a cancelled context time
// to show, then ends and reports whether its context was cancelled.
type outlivingCreateService struct {
	*fakeTaskService

	resume chan struct{}
	done   chan error
}

func (s *outlivingCreateService) CreateTaskStream(
	ctx context.Context,
	_ core.CreateTaskInput,
) (<-chan core.TaskCreateEvent, error) {
	progress := core.TaskCreateEvent{
		Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressCreatingWorktree},
	}
	events := make(chan core.TaskCreateEvent, 8)
	go func() {
		defer close(events)
		events <- progress
		<-s.resume
		events <- progress
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
		events <- core.TaskCreateEvent{Task: &core.Task{ID: "task-1"}}
		s.done <- ctx.Err()
	}()
	return events, nil
}

// A creation runs to its end after the frontend that started it goes away,
// as when the user quits rig while a task is being created.
func TestCreateTaskStreamOutlivesItsFrontend(t *testing.T) {
	t.Parallel()

	svc := &outlivingCreateService{
		fakeTaskService: newFakeTaskService(),
		resume:          make(chan struct{}),
		done:            make(chan error, 1),
	}
	client := startTestFrontend(t, svc)

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", client.socketPath)
	require.NoError(t, err)
	payload, err := json.Marshal(core.CreateTaskInput{Cwd: "/tmp/repo", Prompt: "add retries"})
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(conn).Encode(socketRequest{Command: socketCommandCreateTask, Payload: payload}))
	var first socketEnvelope
	require.NoError(t, json.NewDecoder(conn).Decode(&first))
	require.NoError(t, conn.Close())
	close(svc.resume)

	select {
	case err := <-svc.done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the creation stalled once its frontend went away")
	}
}
