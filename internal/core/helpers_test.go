package core

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
)

type testTaskServiceHarness struct {
	service *service
	// creation and launcher are the same instances the service delegates to,
	// exposed so seam tests can drive the modules through their own interfaces.
	creation    *taskCreation
	launcher    *sessionLauncher
	observation *taskObservation
	models      *providerModelCatalog
	events      []string

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
	claudeClientMock      *MockProviderClient
	claudeRepo            providerClientState
	workspaceMock         *MockTaskWorkspaceManager
	workspace             workspaceManagerState
	userConfigMock        *MockUserConfigStore
	userConfig            userConfigState
	// warnings are the problems the service reported without failing.
	warnings []error
}

type userConfigState struct {
	getErr     error
	saveErr    error
	setup      *ProviderSetup
	savedSetup *ProviderSetup
	// repoDirs are the saved repo directories, which SaveRepoDirs replaces.
	repoDirs        []string
	getRepoDirsErr  error
	saveRepoDirsErr error
	repoDirsSaves   int
	// launchDefaults are what GetLaunchDefaults returns.
	launchDefaults       map[Provider]LaunchChoice
	getLaunchDefaultsErr error
	saveLaunchDefaultErr error
	// savedLaunchDefaults are the SaveLaunchDefault calls, in order, failed
	// ones included.
	savedLaunchDefaults []savedLaunchDefault
}

type savedLaunchDefault struct {
	provider Provider
	choice   LaunchChoice
}

type taskRepositoryState struct {
	healthErr              error
	listErr                error
	createErr              error
	updateErr              error
	deleteErr              error
	updateErrAt            int
	updateCount            int
	listTasks              []*Task
	createdTask            *Task
	updatedTask            *Task
	deletedTaskID          string
	savedProviderSessions  []TaskProviderSession
	providerSessionsByTask map[string][]TaskProviderSession
	activityByTask         map[string][]TaskActivityEvent
	mu                     sync.Mutex
	// agentSessions holds every agent session ever created, open or ended.
	agentSessions []AgentSession
	// createAgentSessionErr fails every agent session creation.
	createAgentSessionErr error
	// agentSessionReads counts reads of each task's open agent sessions.
	agentSessionReads map[string]int
	// taskBranches are the branches recorded for each task.
	taskBranches map[string][]string
	// recordedTaskBranches are the branches each RecordTaskBranches call
	// recorded, in call order.
	recordedTaskBranches [][]string
	listTaskBranchesErr  error
	recordTaskBranchErr  error
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
	// detectedCwds are the paths DetectRepo was asked about, in order.
	detectedCwds []string
	// checkouts are the primary checkouts ListRepoCheckouts finds in each
	// directory. A directory missing from it does not exist, unless
	// checkoutErrs fails its listing.
	checkouts    map[string][]string
	checkoutErrs map[string]error
	// worktreeBranches are the branches ListWorktreeBranches finds in each
	// worktree. A worktree missing from it does not exist, unless
	// worktreeBranchErr fails every listing.
	worktreeBranches  map[string][]string
	worktreeBranchErr error
	// unshippedWork is the unshipped work UnshippedWork finds in each
	// worktree. A worktree missing from it does not exist, unless
	// unshippedWorkErr fails every lookup.
	unshippedWork    map[string]UnshippedWork
	unshippedWorkErr error
	// unshippedWorkLookups are the worktree and shipped work of each
	// UnshippedWork call, in call order.
	unshippedWorkLookups []unshippedWorkLookup
}

type unshippedWorkLookup struct {
	worktreePath string
	shipped      ShippedWork
	rigFiles     []string
}

type sessionClientState struct {
	mu              sync.Mutex
	healthErr       error
	startErr        error
	deleteErr       error
	inspectErr      error
	batchInspectErr error
	events          *[]string
	startedTask     *Task
	deletedTask     *Task
	startedLaunch   TaskSessionLaunchSpec
	// startPane is the pane StartTaskSession reports launching in; zero
	// leaves the launch without an agent session until a hook opens one.
	// startOpened runs once the Session is up, before StartTaskSession
	// returns, as the agent starting in its task window would.
	startPane   TmuxPaneRef
	startOpened func(TmuxPaneRef)
	// SplitAgentPane opens panes %100, %101 and so on, on locateServer,
	// unless agentPaneErr fails them, or agentPaneErrByCommand fails the
	// panes whose launch runs that command. agentPaneOpened runs once a pane
	// is open, before SplitAgentPane returns, as the agent starting in it
	// would.
	agentPaneErr          error
	agentPaneErrByCommand map[string]error
	agentPaneOpened       func(TmuxPaneRef)
	agentPanes            []agentPaneCall
	inspectState          TaskSessionRuntimeState
	batchInspectCalls     int
	batchInspectActive    int
	batchInspectMax       int
	batchInspectStarted   chan struct{}
	batchInspectRelease   chan struct{}
	// The InspectTaskSessions and LocateProcessPane fakes report
	// locateServer, locatePanes and locateTaskSessionPanes as the tmux
	// server's state. LocateProcessPane traces each PID to locatePaneByPID
	// through the process chain in locateChainByPID.
	locateErr              error
	locateServer           TmuxServer
	locatePanes            []TmuxPane
	locateTaskSessionPanes map[string][]string
	locatePaneByPID        map[int]string
	locateChainByPID       map[int][]string
	locatedPIDs            []int
	// CloseAgentPane records each pane it closes in closedPanes and takes the
	// pane off the server, unless closePaneErr fails it.
	closePaneErr error
	closedPanes  []TmuxPaneRef
}

// agentPaneCall is one pane SplitAgentPane was asked to split in.
type agentPaneCall struct {
	task   *Task
	beside string
	launch TaskSessionLaunchSpec
	pane   TmuxPaneRef
}

type providerClientState struct {
	mu                  sync.Mutex
	commandName         string
	healthErr           error
	suggestErr          error
	suggestedName       string
	suggestedSuggestion TaskSuggestion
	suggestRepoRoot     string
	// suggestPrompts lists the prompts the provider was asked to name.
	suggestPrompts  []string
	sessionEnvErr   error
	sessionEnvCalls int
	// sessionEnvRan runs as each session environment is ensured, as a hook
	// event arriving meanwhile would.
	sessionEnvRan func()
	// skillInstallErr is what InstallRigSkill returns, and skillInstallCalls
	// counts its calls.
	skillInstallErr   error
	skillInstallCalls int
	// skillCheckErr is what CheckRigSkill returns.
	skillCheckErr    error
	events           *[]string
	bootstrapErr     error
	bootstrapSpec    WorkspaceBootstrapSpec
	bootstrapRequest *Task
	launchErr        error
	launchRequest    TaskSessionLaunchSpec
	// launchTask is the task the last fresh launch spec was built for.
	launchTask           *Task
	reconnectLaunchErr   error
	reconnectLaunch      TaskSessionLaunchSpec
	hookErr              error
	hookUpdate           *TaskStatusUpdate
	hookInput            HookEventInput
	statusRecoveryErr    error
	statusRecoveryUpdate *AgentSessionStatus
	// statusRecoveryByConversation, when set, recovers per Provider session
	// ID instead of statusRecoveryUpdate.
	statusRecoveryByConversation map[string]*AgentSessionStatus
	statusRecoveryCurrent        *AgentSessionStatus
	statusRecoverySessions       []TaskProviderSession
	statusRecoveryStartedAt      time.Time
	statusRecoveryCalls          map[string]int
	statusRecoveryActive         int
	statusRecoveryMax            int
	statusRecoveryStarted        chan struct{}
	statusRecoveryRelease        chan struct{}
	activityErrByTranscript      map[string]error
	activityByTranscript         map[string][]TaskActivityEvent
	activityCalls                []providerActivityCall
	errByTranscript              map[string]error
	usageByTranscript            map[string]*SessionTokenUsage
	tokenUsageCalls              []providerTokenUsageCall
	// conversationUsageBySession is what ReadConversationUsage returns for a
	// conversation, by its Provider session ID.
	conversationUsageBySession map[string]ConversationUsage
	conversationUsageErr       error
	conversationUsageCalls     [][]TaskProviderSession
	builtLaunchSpecs           []TaskSessionLaunchSpec
}

func (s *providerClientState) mockCommandName() string {
	if s.commandName != "" {
		return s.commandName
	}
	return "codex"
}

type pullRequestClientState struct {
	healthErr            error
	listErr              error
	listRepoPullRequests []RepoPullRequest
	lastListRepoRoot     string
	// branchPullRequests are each branch's pull requests, and
	// branchPullRequestErrs fail the listing of a branch.
	branchPullRequests    map[string][]PullRequest
	branchPullRequestErrs map[string]error
	// branchListings are the repository root and branch of each
	// ListBranchPullRequests call, joined by a colon, in call order.
	branchListings []string
	// defaultBranch is each repository's default branch, unless
	// defaultBranchErr fails its lookup.
	defaultBranch    string
	defaultBranchErr error
	// defaultBranchRepoRoots are the repository roots of each DefaultBranch
	// call, in call order.
	defaultBranchRepoRoots []string
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
	h.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true}
	h.sessionClient.events = &h.events
	h.providerRepo.events = &h.events
	h.providerRepo.commandName = "codex"
	h.claudeRepo.events = &h.events
	h.claudeRepo.commandName = "claude"
	h.userConfig.setup = &ProviderSetup{
		Configured: []Provider{ProviderCodex},
		Default:    ProviderCodex,
	}
	h.taskRepo.providerSessionsByTask = make(map[string][]TaskProviderSession)
	h.taskRepo.activityByTask = make(map[string][]TaskActivityEvent)
	h.taskRepo.agentSessionReads = make(map[string]int)
	h.taskRepoMock = NewMockTaskRepository(t)
	h.repoClientMock = NewMockGitWorktreeClient(t)
	h.sessionClientMock = NewMockTmuxSessionClient(t)
	h.pullRequestClientMock = NewMockPullRequestClient(t)
	h.providerClientMock = NewMockProviderClient(t)
	h.claudeClientMock = NewMockProviderClient(t)
	h.workspaceMock = NewMockTaskWorkspaceManager(t)
	h.userConfigMock = NewMockUserConfigStore(t)
	h.providerRepo.statusRecoveryCalls = make(map[string]int)
	h.claudeRepo.statusRecoveryCalls = make(map[string]int)
	configureTaskRepositoryMock(h.taskRepoMock, &h.taskRepo)
	configureGitWorktreeMock(h.repoClientMock, &h.repoClient)
	configureTmuxSessionMock(h.sessionClientMock, &h.sessionClient)
	configurePullRequestClientMock(h.pullRequestClientMock, &h.pullRequests)
	configureProviderClientMock(h.providerClientMock, &h.providerRepo)
	configureProviderClientMock(h.claudeClientMock, &h.claudeRepo)
	configureWorkspaceManagerMock(h.workspaceMock, &h.workspace, &h.sessionClient)
	configureUserConfigMock(h.userConfigMock, &h.userConfig)

	h.service = NewTaskService(TaskServiceDependencies{
		Tasks:        h.taskRepoMock,
		GitWorktree:  h.repoClientMock,
		TmuxSession:  h.sessionClientMock,
		PullRequests: h.pullRequestClientMock,
		Providers: map[Provider]ProviderClient{
			ProviderCodex:  h.providerClientMock,
			ProviderClaude: h.claudeClientMock,
		},
		Workspace:            h.workspaceMock,
		EnableWorkspaceSetup: true,
		UserConfig:           h.userConfigMock,
		ReportWarning: func(err error) {
			h.warnings = append(h.warnings, err)
		},
	})
	h.creation = h.service.creation
	h.launcher = h.service.launcher
	h.observation = h.service.observation
	h.models = h.service.models

	return h
}

func configureUserConfigMock(store *MockUserConfigStore, state *userConfigState) {
	store.EXPECT().GetProviderSetup(mock.Anything).RunAndReturn(
		func(context.Context) (*ProviderSetup, error) {
			if state.getErr != nil {
				return nil, state.getErr
			}
			if state.setup == nil {
				return nil, nil
			}
			setup := *state.setup
			setup.Configured = append([]Provider(nil), state.setup.Configured...)
			return &setup, nil
		},
	).Maybe()
	store.EXPECT().SaveProviderSetup(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, setup ProviderSetup) error {
			if state.saveErr != nil {
				return state.saveErr
			}
			saved := setup
			state.savedSetup = &saved
			return nil
		},
	).Maybe()
	store.EXPECT().GetLaunchDefaults(mock.Anything).RunAndReturn(
		func(context.Context) (map[Provider]LaunchChoice, error) {
			if state.getLaunchDefaultsErr != nil {
				return nil, state.getLaunchDefaultsErr
			}
			return maps.Clone(state.launchDefaults), nil
		},
	).Maybe()
	store.EXPECT().SaveLaunchDefault(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, provider Provider, choice LaunchChoice) error {
			state.savedLaunchDefaults = append(state.savedLaunchDefaults, savedLaunchDefault{provider, choice})
			return state.saveLaunchDefaultErr
		},
	).Maybe()
	store.EXPECT().GetRepoDirs(mock.Anything).RunAndReturn(
		func(context.Context) ([]string, error) {
			if state.getRepoDirsErr != nil {
				return nil, state.getRepoDirsErr
			}
			return slices.Clone(state.repoDirs), nil
		},
	).Maybe()
	store.EXPECT().SaveRepoDirs(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, dirs []string) error {
			if state.saveRepoDirsErr != nil {
				return state.saveRepoDirsErr
			}
			state.repoDirs = slices.Clone(dirs)
			state.repoDirsSaves++
			return nil
		},
	).Maybe()
}

func configureGitWorktreeMock(client *MockGitWorktreeClient, state *repoClientState) {
	client.EXPECT().HealthCheck(mock.Anything).RunAndReturn(
		func(context.Context) error {
			return state.healthErr
		},
	).Maybe()
	client.EXPECT().DetectRepo(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, cwd string) (RepoContext, error) {
			state.detectedCwds = append(state.detectedCwds, cwd)
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
	client.EXPECT().ListWorktreeBranches(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, worktreePath string) ([]string, error) {
			if state.worktreeBranchErr != nil {
				return nil, state.worktreeBranchErr
			}
			branches, ok := state.worktreeBranches[worktreePath]
			if !ok {
				return nil, &fs.PathError{Op: "stat", Path: worktreePath, Err: fs.ErrNotExist}
			}
			return slices.Clone(branches), nil
		},
	).Maybe()
	client.EXPECT().UnshippedWork(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, worktreePath string, shipped ShippedWork, rigFiles []string) (UnshippedWork, error) {
			state.unshippedWorkLookups = append(
				state.unshippedWorkLookups,
				unshippedWorkLookup{worktreePath, shipped, rigFiles},
			)
			if state.unshippedWorkErr != nil {
				return UnshippedWork{}, state.unshippedWorkErr
			}
			work, ok := state.unshippedWork[worktreePath]
			if !ok {
				return UnshippedWork{}, &fs.PathError{Op: "stat", Path: worktreePath, Err: fs.ErrNotExist}
			}
			return work, nil
		},
	).Maybe()
	client.EXPECT().ListRepoCheckouts(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, dir string) ([]string, error) {
			if err := state.checkoutErrs[dir]; err != nil {
				return nil, err
			}
			checkouts, ok := state.checkouts[dir]
			if !ok {
				return nil, &fs.PathError{Op: "open", Path: dir, Err: fs.ErrNotExist}
			}
			return slices.Clone(checkouts), nil
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
	client.EXPECT().ListBranchPullRequests(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, repoRoot string, branchName string) ([]PullRequest, error) {
			state.branchListings = append(state.branchListings, repoRoot+":"+branchName)
			if err := state.branchPullRequestErrs[branchName]; err != nil {
				return nil, err
			}
			return slices.Clone(state.branchPullRequests[branchName]), nil
		},
	).Maybe()
	client.EXPECT().DefaultBranch(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, repoRoot string) (string, error) {
			state.defaultBranchRepoRoots = append(state.defaultBranchRepoRoots, repoRoot)
			if state.defaultBranchErr != nil {
				return "", state.defaultBranchErr
			}
			return state.defaultBranch, nil
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
		func(_ context.Context, task *Task, launch TaskSessionLaunchSpec) (TmuxPaneRef, error) {
			if state.events != nil {
				*state.events = append(*state.events, "start_task_session")
			}
			state.startedTask = cloneTask(task)
			state.startedLaunch = launch
			if state.startErr != nil {
				return TmuxPaneRef{}, state.startErr
			}
			if state.startOpened != nil {
				state.startOpened(state.startPane)
			}
			return state.startPane, nil
		},
	).Maybe()
	client.EXPECT().SplitAgentPane(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task, beside string, launch TaskSessionLaunchSpec) (TmuxPaneRef, error) {
			state.mu.Lock()
			if state.events != nil {
				*state.events = append(*state.events, "split_agent_pane")
			}
			if state.agentPaneErr != nil {
				state.mu.Unlock()
				return TmuxPaneRef{}, state.agentPaneErr
			}
			if len(launch.Command) > 0 {
				if err := state.agentPaneErrByCommand[launch.Command[0]]; err != nil {
					state.mu.Unlock()
					return TmuxPaneRef{}, err
				}
			}
			pane := TmuxPaneRef{Server: state.locateServer, ID: fmt.Sprintf("%%%d", 100+len(state.agentPanes))}
			state.agentPanes = append(state.agentPanes, agentPaneCall{
				task:   cloneTask(task),
				beside: beside,
				launch: launch,
				pane:   pane,
			})
			opened := state.agentPaneOpened
			state.mu.Unlock()
			if opened != nil {
				opened(pane)
			}
			return pane, nil
		},
	).Maybe()
	client.EXPECT().AttachTaskSession(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	client.EXPECT().InspectTaskSession(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, _ *Task) (TaskSessionRuntimeState, error) {
			return state.inspectState, state.inspectErr
		},
	).Maybe()
	client.EXPECT().InspectTaskSessions(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, tasks []*Task) (TmuxSnapshot, error) {
			state.mu.Lock()
			state.batchInspectCalls++
			state.batchInspectActive++
			if state.batchInspectActive > state.batchInspectMax {
				state.batchInspectMax = state.batchInspectActive
			}
			started := state.batchInspectStarted
			release := state.batchInspectRelease
			inspectErr := state.batchInspectErr
			state.mu.Unlock()
			if started != nil {
				select {
				case started <- struct{}{}:
				default:
				}
			}
			if release != nil {
				<-release
			}
			state.mu.Lock()
			state.batchInspectActive--
			state.mu.Unlock()
			if inspectErr != nil {
				return TmuxSnapshot{}, inspectErr
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			snapshot := TmuxSnapshot{
				TaskSessionPanes: make(map[string][]string, len(tasks)),
				Server:           state.locateServer,
				Panes:            append([]TmuxPane(nil), state.locatePanes...),
			}
			for _, task := range tasks {
				if task == nil {
					continue
				}
				if panes, ok := state.locateTaskSessionPanes[task.ID]; ok {
					snapshot.TaskSessionPanes[task.ID] = append([]string(nil), panes...)
				}
			}
			return snapshot, nil
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
	client.EXPECT().CloseAgentPane(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, pane TmuxPaneRef) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.closePaneErr != nil {
				return state.closePaneErr
			}
			state.closedPanes = append(state.closedPanes, pane)
			state.locatePanes = slices.DeleteFunc(state.locatePanes, func(open TmuxPane) bool {
				return open.ID == pane.ID
			})
			for taskID, panes := range state.locateTaskSessionPanes {
				state.locateTaskSessionPanes[taskID] = slices.DeleteFunc(panes, func(open string) bool {
					return open == pane.ID
				})
			}
			return nil
		},
	).Maybe()
	client.EXPECT().LocateProcessPane(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, task *Task, pid int) (TmuxSnapshot, ProcessPane, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.locatedPIDs = append(state.locatedPIDs, pid)
			if state.locateErr != nil {
				return TmuxSnapshot{}, ProcessPane{}, state.locateErr
			}
			snapshot := TmuxSnapshot{
				TaskSessionPanes: map[string][]string{},
				Server:           state.locateServer,
				Panes:            append([]TmuxPane(nil), state.locatePanes...),
			}
			if task != nil {
				if panes, ok := state.locateTaskSessionPanes[task.ID]; ok {
					snapshot.TaskSessionPanes[task.ID] = append([]string(nil), panes...)
				}
			}
			if pid <= 0 || state.locatePaneByPID[pid] == "" {
				return snapshot, ProcessPane{}, nil
			}
			return snapshot, ProcessPane{
				Pane:  state.locatePaneByPID[pid],
				Chain: append([]string(nil), state.locateChainByPID[pid]...),
			}, nil
		},
	).Maybe()
}

func configureProviderClientMock(client *MockProviderClient, state *providerClientState) {
	client.EXPECT().Doctor(mock.Anything).RunAndReturn(
		func(context.Context) error {
			return state.healthErr
		},
	).Maybe()
	client.EXPECT().SuggestTaskName(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, repoRoot string, prompt string) (TaskSuggestion, error) {
			state.suggestRepoRoot = repoRoot
			state.suggestPrompts = append(state.suggestPrompts, prompt)
			if state.suggestErr != nil {
				return TaskSuggestion{}, state.suggestErr
			}
			if state.suggestedSuggestion != (TaskSuggestion{}) {
				return state.suggestedSuggestion, nil
			}
			return TaskSuggestion{Name: state.suggestedName, BranchType: "feat"}, nil
		},
	).Maybe()
	client.EXPECT().EnsureTaskSessionEnvironment(mock.Anything).RunAndReturn(
		func(context.Context) error {
			if state.events != nil {
				*state.events = append(*state.events, "ensure_task_session_environment")
			}
			state.sessionEnvCalls++
			if state.sessionEnvRan != nil {
				state.sessionEnvRan()
			}
			return state.sessionEnvErr
		},
	).Maybe()
	client.EXPECT().InstallRigSkill(mock.Anything).RunAndReturn(
		func(context.Context) error {
			state.skillInstallCalls++
			return state.skillInstallErr
		},
	).Maybe()
	client.EXPECT().CheckRigSkill(mock.Anything).RunAndReturn(
		func(context.Context) error {
			return state.skillCheckErr
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
			state.launchTask = cloneTask(task)
			if state.launchErr != nil {
				return TaskSessionLaunchSpec{}, state.launchErr
			}
			if hasCustomLaunchSpec(state.launchRequest) {
				return state.launchRequest, nil
			}
			launch := TaskSessionLaunchSpec{
				Command:     []string{state.mockCommandName()},
				ReadyMarker: "›",
			}
			if task.Prompt != "" {
				launch.PrefillInput = []string{task.Prompt}
			}
			state.builtLaunchSpecs = append(state.builtLaunchSpecs, launch)
			return launch, nil
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
				Command:     []string{state.mockCommandName(), "resume", sessionID},
				ReadyMarker: "›",
			}, nil
		},
	).Maybe()
	client.EXPECT().TaskSessionCommandName().RunAndReturn(state.mockCommandName).Maybe()
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
	client.EXPECT().RecoverAgentSessionStatus(mock.Anything, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(
			_ context.Context,
			current AgentSessionStatus,
			conversation []TaskProviderSession,
			providerStartedAt time.Time,
		) (*AgentSessionStatus, error) {
			state.mu.Lock()
			copyCurrent := current
			state.statusRecoveryCurrent = &copyCurrent
			state.statusRecoverySessions = append([]TaskProviderSession(nil), conversation...)
			state.statusRecoveryStartedAt = providerStartedAt
			// Core asks for recovery only with a conversation's history, whose
			// entries all belong to the agent session's task.
			state.statusRecoveryCalls[conversation[0].TaskID]++
			state.statusRecoveryActive++
			if state.statusRecoveryActive > state.statusRecoveryMax {
				state.statusRecoveryMax = state.statusRecoveryActive
			}
			started := state.statusRecoveryStarted
			release := state.statusRecoveryRelease
			recoveryErr := state.statusRecoveryErr
			recoveryUpdate := state.statusRecoveryUpdate
			if state.statusRecoveryByConversation != nil {
				recoveryUpdate = state.statusRecoveryByConversation[conversation[0].ProviderSessionID]
			}
			state.mu.Unlock()
			if started != nil {
				select {
				case started <- struct{}{}:
				default:
				}
			}
			if release != nil {
				<-release
			}
			state.mu.Lock()
			state.statusRecoveryActive--
			state.mu.Unlock()
			if recoveryErr != nil {
				return nil, recoveryErr
			}
			if recoveryUpdate == nil {
				return nil, nil
			}
			recovered := *recoveryUpdate
			return &recovered, nil
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
	client.EXPECT().ReadConversationUsage(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, conversation []TaskProviderSession) (ConversationUsage, error) {
			state.conversationUsageCalls = append(state.conversationUsageCalls, conversation)
			if state.conversationUsageErr != nil {
				return ConversationUsage{}, state.conversationUsageErr
			}
			return state.conversationUsageBySession[conversation[0].ProviderSessionID], nil
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
			// A listed task reads back as updated.
			for i, listed := range state.listTasks {
				if listed != nil && task != nil && listed.ID == task.ID {
					state.listTasks[i] = cloneTask(task)
				}
			}
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
			defer state.mu.Unlock()
			remaining := state.agentSessions[:0]
			for _, session := range state.agentSessions {
				if session.TaskID != taskID {
					remaining = append(remaining, session)
				}
			}
			state.agentSessions = remaining
			return nil
		},
	).Maybe()
	repo.EXPECT().ListTasks(mock.Anything).RunAndReturn(
		func(context.Context) ([]*Task, error) {
			if state.listErr != nil {
				return nil, state.listErr
			}
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
	repo.EXPECT().ListLatestAgentSessionPrompts(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) ([]TaskActivityEvent, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			open := make(map[string]bool)
			for _, session := range state.agentSessions {
				if session.TaskID == taskID && session.IsOpen() {
					open[session.ID] = true
				}
			}
			latest := make(map[string]TaskActivityEvent)
			for _, event := range state.activityByTask[taskID] {
				if !open[event.AgentSessionID] || event.Role != TaskActivityRoleUser {
					continue
				}
				if current, ok := latest[event.AgentSessionID]; !ok || !event.ObservedAt.Before(current.ObservedAt) {
					latest[event.AgentSessionID] = event
				}
			}
			prompts := make([]TaskActivityEvent, 0, len(latest))
			for _, event := range latest {
				prompts = append(prompts, event)
			}
			return prompts, nil
		},
	).Maybe()
	repo.EXPECT().ListAgentSessionPrompts(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, agentSessionID string) ([]TaskActivityEvent, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			var prompts []TaskActivityEvent
			for _, events := range state.activityByTask {
				for _, event := range events {
					if agentSessionID != "" && event.AgentSessionID == agentSessionID &&
						event.Role == TaskActivityRoleUser {
						prompts = append(prompts, event)
					}
				}
			}
			slices.SortStableFunc(prompts, func(left, right TaskActivityEvent) int {
				return left.ObservedAt.Compare(right.ObservedAt)
			})
			return prompts, nil
		},
	).Maybe()
	repo.EXPECT().UpsertTaskProviderSession(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, session TaskProviderSession) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.savedProviderSessions = append(state.savedProviderSessions, session)
			state.providerSessionsByTask[session.TaskID] = append(
				state.providerSessionsByTask[session.TaskID],
				session,
			)
			return nil
		},
	).Maybe()
	repo.EXPECT().ListTaskProviderSessions(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) ([]TaskProviderSession, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			return append([]TaskProviderSession(nil), state.providerSessionsByTask[taskID]...), nil
		},
	).Maybe()
	configureAgentSessionRepositoryMock(repo, state)
}

// configureAgentSessionRepositoryMock stores agent sessions in memory and
// enforces the repository's one-open-agent-session-per-pane rule.
func configureAgentSessionRepositoryMock(repo *MockTaskRepository, state *taskRepositoryState) {
	repo.EXPECT().CreateAgentSession(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, session AgentSession) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.createAgentSessionErr != nil {
				return state.createAgentSessionErr
			}
			for _, existing := range state.agentSessions {
				if existing.ID == session.ID {
					return fmt.Errorf("agent session %q already exists", session.ID)
				}
				if existing.IsOpen() && session.TmuxPane != "" &&
					existing.TmuxServer == session.TmuxServer && existing.TmuxPane == session.TmuxPane {
					return fmt.Errorf("pane %s already has open agent session %q", session.TmuxPane, existing.ID)
				}
			}
			state.agentSessions = append(state.agentSessions, session)
			return nil
		},
	).Maybe()
	repo.EXPECT().OpenAgentSessionOnPane(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, server TmuxServer, pane string) (*AgentSession, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			for _, existing := range state.agentSessions {
				if existing.IsOpen() && pane != "" && existing.TmuxServer == server && existing.TmuxPane == pane {
					found := existing
					return &found, nil
				}
			}
			return nil, nil
		},
	).Maybe()
	repo.EXPECT().ListOpenAgentSessions(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) ([]AgentSession, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.agentSessionReads[taskID]++
			var open []AgentSession
			for _, existing := range state.agentSessions {
				if existing.TaskID == taskID && existing.IsOpen() {
					open = append(open, existing)
				}
			}
			slices.SortStableFunc(open, func(left, right AgentSession) int {
				return left.StartedAt.Compare(right.StartedAt)
			})
			return open, nil
		},
	).Maybe()
	repo.EXPECT().LatestAgentSession(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) (*AgentSession, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			var latest *AgentSession
			for i, existing := range state.agentSessions {
				if existing.TaskID == taskID && (latest == nil || !existing.StartedAt.Before(latest.StartedAt)) {
					latest = &state.agentSessions[i]
				}
			}
			if latest == nil {
				return nil, nil
			}
			found := *latest
			return &found, nil
		},
	).Maybe()
	repo.EXPECT().UpdateAgentSession(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, session AgentSession) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			for i, existing := range state.agentSessions {
				if existing.ID != session.ID || !existing.IsOpen() {
					continue
				}
				existing.TmuxServer = session.TmuxServer
				existing.TmuxPane = session.TmuxPane
				existing.LaunchedAt = session.LaunchedAt
				existing.PaneTrusted = session.PaneTrusted
				existing.ProviderSessionID = session.ProviderSessionID
				existing.Status = session.Status
				existing.Name = session.Name
				existing.NamedProviderSessionID = session.NamedProviderSessionID
				state.agentSessions[i] = existing
			}
			return nil
		},
	).Maybe()
	repo.EXPECT().EndAgentSession(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, agentSessionID string, endedAt time.Time) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			for i, existing := range state.agentSessions {
				if existing.ID == agentSessionID && existing.IsOpen() {
					state.agentSessions[i].EndedAt = endedAt
				}
			}
			return nil
		},
	).Maybe()
	repo.EXPECT().ListTaskBranches(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) ([]string, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.listTaskBranchesErr != nil {
				return nil, state.listTaskBranchesErr
			}
			branches := slices.Clone(state.taskBranches[taskID])
			slices.Sort(branches)
			return branches, nil
		},
	).Maybe()
	repo.EXPECT().RecordTaskBranches(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string, branchNames []string) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.recordTaskBranchErr != nil {
				return state.recordTaskBranchErr
			}
			state.recordedTaskBranches = append(state.recordedTaskBranches, slices.Clone(branchNames))
			if state.taskBranches == nil {
				state.taskBranches = map[string][]string{}
			}
			for _, branch := range branchNames {
				if !slices.Contains(state.taskBranches[taskID], branch) {
					state.taskBranches[taskID] = append(state.taskBranches[taskID], branch)
				}
			}
			return nil
		},
	).Maybe()
}

// taskLevelStatus is an update without the agent sessions it carries: the
// status the task row shows.
func taskLevelStatus(update TaskStatusUpdate) TaskStatusUpdate {
	update.LeadAgentSessionID = ""
	update.AgentSessions = nil
	return update
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
