package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"rig/internal/adapters/handler/tui"
	"rig/internal/adapters/repository/sqlite"
	"rig/internal/adapters/taskdaemon"
	"rig/internal/core"
	"rig/internal/infrastructure"
	"rig/internal/pkg/subprocess"

	codexprovider "rig/internal/adapters/client/codexprovider"
	gitworktree "rig/internal/adapters/client/gitworktree"
	tmuxsession "rig/internal/adapters/client/tmuxsession"

	repositoryworkspace "rig/internal/adapters/repository/workspace"

	tea "charm.land/bubbletea/v2"
)

const (
	// The task daemon is a re-executed child of the same rig binary. The
	// client invocation sets this env var on the spawned child so execute()
	// can choose daemon serving instead of the normal TUI flow.
	daemonModeEnvKey   = "RIG_MODE"
	daemonModeEnvValue = "task-daemon"
)

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

	// The daemon is not a separate executable. EnsureRunning re-execs this same
	// binary with RIG_MODE=task-daemon, and that child process takes this path.
	if os.Getenv(daemonModeEnvKey) == daemonModeEnvValue {
		return serveTaskDaemon(ctx, cfg, execPath, sourceRoot, cancel)
	}

	adapter := taskdaemon.New(taskdaemon.Config{
		SocketPath:     cfg.Daemon.SocketPath,
		HookListenAddr: cfg.Daemon.HookListenAddr,
		ExecPath:       execPath,
		Env: []string{
			// Passed to the re-executed child so it serves the daemon instead of
			// recursively trying to ensure another daemon and launch the TUI.
			daemonModeEnvKey + "=" + daemonModeEnvValue,
		},
	})

	if err := adapter.EnsureRunning(ctx); err != nil {
		return err
	}

	frontend := adapter.Frontend()
	if frontend == nil {
		return fmt.Errorf("task frontend not configured")
	}

	program := tui.NewProgram(
		frontend,
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)
	_, err = program.Run()
	return err
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

	// TODO: why not just
	// taskRepo, err := sqlite.New(sqlite.Config{Path: cfg.SQLite.Path})
	taskRepo, err := sqlite.New(cfg.SQLite)
	if err != nil {
		return err
	}

	runner := subprocess.ExecRunner{}

	providers := map[core.Provider]core.ProviderClient{
		core.ProviderCodex: codexprovider.New(
			runner,
			cfg.Codex,
			codexprovider.HookForwardingConfig{
				RigBinaryPath: execPath,
				SourceRoot:    sourceRoot,
			},
		),
	}

	service := core.NewTaskService(core.TaskServiceDependencies{
		Tasks:                taskRepo,
		GitWorktree:          gitworktree.New(runner),
		TmuxSession:          tmuxsession.New(runner),
		Providers:            providers,
		Workspace:            repositoryworkspace.New(),
		EnableWorkspaceSetup: true,
		DefaultProvider:      cfg.Provider,
	})

	codexHooks := codexprovider.NewHookHTTPHandler(service, nil)
	adapter := taskdaemon.New(cfg.Daemon)

	return adapter.Serve(ctx, service, []core.TaskDaemonHookRoute{
		{Path: "/hook", Handler: codexHooks},
		{Path: "/codex-hook", Handler: codexHooks},
	},
		stop)
}
