package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"rig/internal/adapters/client/codex"
	"rig/internal/adapters/client/git"
	"rig/internal/adapters/client/tmux"
	"rig/internal/adapters/handler/tui"
	"rig/internal/adapters/repository/sqlite"
	"rig/internal/adapters/taskdaemon"
	"rig/internal/core"
	"rig/internal/infrastructure"
	"rig/internal/pkg/subprocess"

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

var version = "dev"

func main() {
	if err := executeWithArgs(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func executeWithArgs(args []string, stdout io.Writer, _ io.Writer) error {
	if len(args) == 1 && args[0] == "--version" {
		if stdout == nil {
			stdout = os.Stdout
		}
		_, err := fmt.Fprintln(stdout, version)
		return err
	}

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

	taskRepo, err := sqlite.New(cfg.SQLite)
	if err != nil {
		return err
	}

	runner := subprocess.ExecRunner{}

	providers := map[core.Provider]core.ProviderClient{
		core.ProviderCodex: codex.New(
			runner,
			cfg.Codex,
			codex.HookForwardingConfig{
				CollectorURL:  "http://" + cfg.Daemon.HookListenAddr + "/codex-hook",
				RigBinaryPath: execPath,
				SourceRoot:    sourceRoot,
			},
		),
	}

	service := core.NewTaskService(core.TaskServiceDependencies{
		Tasks:                taskRepo,
		GitWorktree:          git.New(runner),
		TmuxSession:          tmux.New(runner),
		Providers:            providers,
		Workspace:            repositoryworkspace.New(),
		EnableWorkspaceSetup: true,
		DefaultProvider:      cfg.Provider,
	})

	codexHooks := codex.NewHookHTTPHandler(service, nil)
	adapter := taskdaemon.New(cfg.Daemon)

	return adapter.Serve(ctx, service, []core.TaskDaemonHookRoute{
		{Path: "/hook", Handler: codexHooks},
		{Path: "/codex-hook", Handler: codexHooks},
	},
		stop)
}
