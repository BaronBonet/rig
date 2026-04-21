package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"rig/internal/adapters/handler/tui"
	"rig/internal/adapters/taskdaemon"
	"rig/internal/core"
	"rig/internal/infrastructure"
	"rig/internal/pkg/subprocess"

	claudeclient "rig/internal/adapters/client/claude"
	claudeagent "rig/internal/adapters/client/claudeagent"
	codexagent "rig/internal/adapters/client/codexagent"
	gitworktree "rig/internal/adapters/client/gitworktree"
	tmuxsession "rig/internal/adapters/client/tmuxsession"

	claudehooks "rig/internal/adapters/observability/claudehooks"
	tasksqlite "rig/internal/adapters/repository/tasksqlite"
	repositoryworkspace "rig/internal/adapters/repository/workspace"

	tea "charm.land/bubbletea/v2"
)

const (
	daemonModeEnvKey   = "RIG_MODE"
	daemonModeEnvValue = "task-daemon"
)

type dependencies struct {
	Daemon taskDaemonRuntime
	RunUI  func(core.TaskFrontend) error
}

type taskDaemonRuntime interface {
	EnsureRunning(context.Context) error
	Frontend() core.TaskFrontend
}

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute() error {
	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		return err
	}

	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	sourceRoot, err := os.Getwd()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// TODO: what is the point of this? don't we always want to serve teh task daemon?
	if isDaemonMode() {
		return serveTaskDaemon(ctx, cfg, execPath, sourceRoot, cancel)
	}

	deps, err := newRuntimeDependencies(cfg, execPath)
	if err != nil {
		return err
	}

	return run(ctx, deps)
}

func run(ctx context.Context, deps dependencies) error {
	if deps.Daemon == nil {
		return fmt.Errorf("task daemon not configured")
	}
	if deps.RunUI == nil {
		return fmt.Errorf("task tui runner not configured")
	}

	frontend := deps.Daemon.Frontend()
	if frontend == nil {
		return fmt.Errorf("task frontend not configured")
	}

	if err := deps.Daemon.EnsureRunning(ctx); err != nil {
		return err
	}

	return deps.RunUI(frontend)
}

func newRuntimeDependencies(cfg *infrastructure.ApplicationConfig, execPath string) (dependencies, error) {
	daemon := taskdaemon.New(taskdaemon.Config{
		SocketPath:     cfg.TaskDaemon.SocketPath,
		HookListenAddr: cfg.TaskDaemon.HookListenAddr,
		ExecPath:       execPath,
		Env: []string{
			daemonModeEnvKey + "=" + daemonModeEnvValue,
		},
	})

	return dependencies{
		Daemon: daemon,
		RunUI: func(frontend core.TaskFrontend) error {
			program := tui.NewProgram(
				frontend,
				tea.WithInput(os.Stdin),
				tea.WithOutput(os.Stdout),
			)
			_, err := program.Run()
			return err
		},
	}, nil
}

func serveTaskDaemon(
	ctx context.Context,
	cfg *infrastructure.ApplicationConfig,
	execPath string,
	sourceRoot string,
	stop func(),
) error {
	if cfg == nil {
		return fmt.Errorf("application config not configured")
	}

	service, err := newTaskService(cfg, execPath, sourceRoot)
	if err != nil {
		return err
	}

	hookRoutes := daemonHookRoutes(service)
	adapter := taskdaemon.New(cfg.TaskDaemon)
	return adapter.Serve(ctx, service, hookRoutes, stop)
}

func daemonHookRoutes(service core.TaskService) []taskdaemon.HookRoute {
	codexHooks := codexagent.NewHookHTTPHandler(service, nil)
	claudeHooks := claudehooks.NewHTTPHandler(taskServiceHookIngestor{service: service}, nil)

	return []taskdaemon.HookRoute{
		{Path: "/hook", Handler: codexHooks},
		{Path: "/codex-hook", Handler: codexHooks},
		{Path: "/claude-hook", Handler: claudeHooks},
	}
}

type taskServiceHookIngestor struct {
	service core.TaskService
}

func (i taskServiceHookIngestor) IngestHookEvent(
	ctx context.Context,
	input core.HookEventInput,
) (*core.HookSessionSummary, error) {
	if i.service == nil {
		return nil, fmt.Errorf("task service not configured")
	}

	if err := i.service.HandleHookEvent(ctx, input); err != nil {
		return nil, err
	}

	return nil, nil
}

func newTaskService(
	cfg *infrastructure.ApplicationConfig,
	execPath string,
	sourceRoot string,
) (core.TaskService, error) {
	if cfg == nil {
		return nil, fmt.Errorf("application config not configured")
	}

	runner := subprocess.ExecRunner{}
	taskRepo, err := tasksqlite.New(tasksqlite.Config{Path: cfg.TaskSQLite.Path})
	if err != nil {
		return nil, err
	}

	agents := map[core.AgentProvider]core.AgentClient{
		core.AgentProviderCodex: codexagent.New(
			runner,
			cfg.Codex,
			codexagent.HookForwardingConfig{
				RigBinaryPath: execPath,
				SourceRoot:    sourceRoot,
			},
		),
		core.AgentProviderClaude: claudeagent.New(runner, claudeclient.Config{
			Binary:         cfg.Claude.Binary,
			HookListenAddr: cfg.TaskDaemon.HookListenAddr,
		}),
	}

	return core.NewTaskService(core.TaskServiceDependencies{
		Tasks:                taskRepo,
		GitWorktree:          gitworktree.New(runner),
		TmuxSession:          tmuxsession.New(runner),
		Agents:               agents,
		Workspace:            repositoryworkspace.New(),
		EnableWorkspaceSetup: true,
		DefaultProvider:      cfg.Provider,
	}), nil
}

func isDaemonMode() bool {
	return os.Getenv(daemonModeEnvKey) == daemonModeEnvValue
}
