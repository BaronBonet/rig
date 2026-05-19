package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/BaronBonet/rig/internal/adapters/client/codex"
	"github.com/BaronBonet/rig/internal/adapters/client/git"
	"github.com/BaronBonet/rig/internal/adapters/client/github"
	"github.com/BaronBonet/rig/internal/adapters/client/tmux"
	"github.com/BaronBonet/rig/internal/adapters/handler/tui"
	"github.com/BaronBonet/rig/internal/adapters/repository/sqlite"
	"github.com/BaronBonet/rig/internal/adapters/taskdaemon"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/infrastructure"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	repositoryworkspace "github.com/BaronBonet/rig/internal/adapters/repository/workspace"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
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

func executeWithArgs(args []string, stdout io.Writer, stderr io.Writer) error {
	cmd := newRootCommand(newProductionCommandRuntime(stdout, stderr))
	cmd.SetArgs(args)
	return cmd.Execute()
}

func newProductionCommandRuntime(stdout io.Writer, stderr io.Writer) commandRuntime {
	return commandRuntime{
		stdout:  stdout,
		stderr:  stderr,
		version: version,
		runTUI: func() error {
			return runTUI(stdout)
		},
		runDoctor: func() error {
			cfg, err := infrastructure.LoadConfig()
			if err != nil {
				return err
			}

			return runDoctor(stdout, cfg)
		},
		runDaemonStart: func() error {
			return runDaemonLifecycleCommand(
				stdout,
				"running",
				func(ctx context.Context, daemon core.TaskDaemon) error {
					return daemon.EnsureRunning(ctx)
				},
			)
		},
		runDaemonStop: func() error {
			return runDaemonLifecycleCommand(
				stdout,
				"stopped",
				func(ctx context.Context, daemon core.TaskDaemon) error {
					return daemon.Stop(ctx)
				},
			)
		},
		runDaemonRestart: func() error {
			return runDaemonLifecycleCommand(
				stdout,
				"restarted",
				func(ctx context.Context, daemon core.TaskDaemon) error {
					return daemon.Restart(ctx)
				},
			)
		},
		runDaemonStatus: func() error {
			if stdout == nil {
				stdout = os.Stdout
			}

			cfg, err := infrastructure.LoadConfig()
			if err != nil {
				return err
			}
			daemon, err := newClientTaskDaemon(cfg)
			if err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			status, err := daemon.Status(ctx)
			if err != nil {
				return err
			}
			renderDaemonStatus(stdout, status)
			return nil
		},
	}
}

type commandRuntime struct {
	stdout  io.Writer
	stderr  io.Writer
	version string

	runTUI           func() error
	runDoctor        func() error
	runDaemonStart   func() error
	runDaemonStop    func() error
	runDaemonRestart func() error
	runDaemonStatus  func() error
}

func newRootCommand(runtime commandRuntime) *cobra.Command {
	if runtime.stdout == nil {
		runtime.stdout = os.Stdout
	}
	if runtime.stderr == nil {
		runtime.stderr = os.Stderr
	}

	root := &cobra.Command{
		Use:           "rig",
		Short:         "Manage AI-assisted coding tasks",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       runtime.version,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runCommand(runtime.runTUI)
		},
	}
	root.SetOut(runtime.stdout)
	root.SetErr(runtime.stderr)
	root.SetVersionTemplate("{{.Version}}\n")

	root.AddCommand(newDoctorCommand(runtime))
	root.AddCommand(newDaemonCommand(runtime))

	return root
}

func newDoctorCommand(runtime commandRuntime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the local rig environment",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runCommand(runtime.runDoctor)
		},
	}
}

func newDaemonCommand(runtime commandRuntime) *cobra.Command {
	daemon := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the rig task daemon",
	}
	daemon.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the task daemon",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runCommand(runtime.runDaemonStart)
		},
	})
	daemon.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the task daemon",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runCommand(runtime.runDaemonStop)
		},
	})
	daemon.AddCommand(&cobra.Command{
		Use:   "restart",
		Short: "Restart the task daemon",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runCommand(runtime.runDaemonRestart)
		},
	})
	daemon.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show task daemon status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runCommand(runtime.runDaemonStatus)
		},
	})

	return daemon
}

func runCommand(run func() error) error {
	if run == nil {
		return fmt.Errorf("command not configured")
	}

	return run()
}

func runDaemonLifecycleCommand(
	stdout io.Writer,
	result string,
	run func(context.Context, core.TaskDaemon) error,
) error {
	if stdout == nil {
		stdout = os.Stdout
	}

	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		return err
	}
	daemon, err := newClientTaskDaemon(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, daemon); err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "Task daemon %s\n", result)
	return err
}

func runTUI(stdout io.Writer) error {
	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		return err
	}

	displayVersion := taskdaemon.FrontendBuildVersion()
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	daemonBuildIdentity, err := taskDaemonBuildIdentity(execPath, version)
	if err != nil {
		return err
	}
	taskdaemon.SetFrontendBuildVersion(daemonBuildIdentity)

	sourceRoot, err := os.Getwd()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// The daemon is not a separate executable. EnsureRunning re-execs this same
	// binary with RIG_MODE=task-daemon, and that child process takes this path.
	if os.Getenv(daemonModeEnvKey) == daemonModeEnvValue {
		return serveTaskDaemon(ctx, cfg, cancel)
	}

	adapter, err := newClientTaskDaemon(cfg)
	if err != nil {
		return err
	}
	if err := adapter.EnsureRunning(ctx); err != nil {
		return err
	}

	frontend := adapter.Frontend()
	if frontend == nil {
		return fmt.Errorf("task frontend not configured")
	}

	if stdout == nil {
		stdout = os.Stdout
	}
	program := tui.NewProgramWithVersion(
		frontend,
		sourceRoot,
		displayVersion,
		tea.WithInput(os.Stdin),
		tea.WithOutput(stdout),
	)
	_, err = program.Run()
	return err
}

func newClientTaskDaemon(cfg *infrastructure.ApplicationConfig) (core.TaskDaemon, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	daemonBuildIdentity, err := taskDaemonBuildIdentity(execPath, version)
	if err != nil {
		return nil, err
	}
	taskdaemon.SetFrontendBuildVersion(daemonBuildIdentity)

	return taskdaemon.New(taskdaemon.Config{
		SocketPath:     cfg.Daemon.SocketPath,
		HookListenAddr: cfg.Daemon.HookListenAddr,
		ExecPath:       execPath,
		Env: []string{
			// Passed to the re-executed child so it serves the daemon instead of
			// recursively trying to ensure another daemon and launch the TUI.
			daemonModeEnvKey + "=" + daemonModeEnvValue,
		},
	}), nil
}

func renderDaemonStatus(stdout io.Writer, status *core.TaskDaemonStatus) {
	if stdout == nil {
		stdout = os.Stdout
	}
	if status == nil {
		fmt.Fprintln(stdout, "Task daemon: unknown")
		return
	}

	fmt.Fprintf(stdout, "Socket: %s\n", status.SocketPath)
	switch {
	case status.Compatible:
		fmt.Fprintln(stdout, "Task daemon: running")
		fmt.Fprintln(stdout, "Health: ok")
		fmt.Fprintln(stdout, "Compatibility: ok")
	case status.Healthy:
		fmt.Fprintln(stdout, "Task daemon: running")
		fmt.Fprintln(stdout, "Health: ok")
		fmt.Fprintln(stdout, "Compatibility: mismatch")
	case status.Running:
		fmt.Fprintln(stdout, "Task daemon: running")
		fmt.Fprintln(stdout, "Health: failed")
	default:
		fmt.Fprintln(stdout, "Task daemon: stopped")
	}

	if status.Error != "" {
		fmt.Fprintf(stdout, "Error: %s\n", status.Error)
	}
}

func runDoctor(stdout io.Writer, cfg *infrastructure.ApplicationConfig) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	if cfg == nil {
		return fmt.Errorf("application config not configured")
	}
	service := newDoctorService(cfg)

	fmt.Fprintln(stdout, "Rig doctor")
	fmt.Fprintf(stdout, "Provider: %s\n", cfg.Provider)
	fmt.Fprintf(stdout, "SQLite: %s\n", cfg.SQLite.Path)
	fmt.Fprintf(stdout, "Daemon socket: %s\n\n", cfg.Daemon.SocketPath)

	checks, err := service.HealthCheck(context.Background())
	renderHealthChecks(stdout, checks)

	return err
}

func newDoctorService(cfg *infrastructure.ApplicationConfig) core.TaskService {
	runner := subprocess.ExecRunner{}
	providers := map[core.Provider]core.ProviderClient{
		core.ProviderCodex: codex.New(
			runner,
			cfg.Codex,
			codex.NewHookForwardingConfig(cfg.Daemon.HookListenAddr, ""),
		),
	}

	return core.NewTaskService(core.TaskServiceDependencies{
		Tasks:           sqlite.NewHealthCheckRepository(cfg.SQLite),
		GitWorktree:     git.New(runner),
		TmuxSession:     tmux.New(runner),
		PullRequests:    github.New(runner),
		Providers:       providers,
		DefaultProvider: cfg.Provider,
	})
}

func renderHealthChecks(stdout io.Writer, checks []core.HealthCheck) {
	for _, check := range checks {
		switch {
		case check.Err == nil:
			fmt.Fprintf(stdout, "OK   %-6s\n", check.Name)
		case check.Required:
			fmt.Fprintf(stdout, "FAIL %-6s %s\n", check.Name, check.Err)
		default:
			fmt.Fprintf(stdout, "WARN %-6s %s\n", check.Name, check.Err)
		}
	}
}

func taskDaemonBuildIdentity(execPath string, version string) (string, error) {
	if version != "" && version != "dev" {
		return version, nil
	}

	info, err := os.Stat(execPath)
	if err != nil {
		return "", fmt.Errorf("stat task daemon executable: %w", err)
	}

	return fmt.Sprintf("dev:%s:%d:%d", execPath, info.Size(), info.ModTime().UnixNano()), nil
}

func serveTaskDaemon(
	ctx context.Context,
	cfg *infrastructure.ApplicationConfig,
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
	hookSecret, err := codex.NewHookSecret()
	if err != nil {
		return err
	}

	providers := map[core.Provider]core.ProviderClient{
		core.ProviderCodex: codex.New(
			runner,
			cfg.Codex,
			codex.NewHookForwardingConfig(cfg.Daemon.HookListenAddr, hookSecret),
		),
	}

	service := core.NewTaskService(core.TaskServiceDependencies{
		Tasks:                taskRepo,
		GitWorktree:          git.New(runner),
		TmuxSession:          tmux.New(runner),
		PullRequests:         github.New(runner),
		Providers:            providers,
		Workspace:            repositoryworkspace.New(),
		EnableWorkspaceSetup: true,
		DefaultProvider:      cfg.Provider,
	})

	adapter := taskdaemon.New(cfg.Daemon)

	return adapter.Serve(ctx, service, codex.NewHookRoutes(service, nil, hookSecret), stop)
}
