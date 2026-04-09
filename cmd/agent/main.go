package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	claudeclient "agent/internal/adapters/client/claude"
	codexclient "agent/internal/adapters/client/codex"
	gitclient "agent/internal/adapters/client/git"
	tmuxclient "agent/internal/adapters/client/tmux"
	agentconfigfs "agent/internal/adapters/filesystem/agentconfig"
	codexhooksfs "agent/internal/adapters/filesystem/codexhooks"
	workspacefs "agent/internal/adapters/filesystem/workspace"
	"agent/internal/adapters/handler/cli"
	observer "agent/internal/adapters/observability/observer"
	sqliterepo "agent/internal/adapters/repository/sqlite"
	"agent/internal/core"
	"agent/internal/infrastructure"
	"agent/internal/pkg/execx"
)

func main() {
	deps, err := buildDependencies()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := cli.NewRootCommand(deps).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func buildDependencies() (cli.Dependencies, error) {
	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		return cli.Dependencies{}, err
	}

	runner := execx.ExecRunner{}

	taskRepo, err := sqliterepo.NewRepository(cfg.SQLite)
	if err != nil {
		return cli.Dependencies{}, err
	}

	agentExec, err := os.Executable()
	if err != nil {
		return cli.Dependencies{}, err
	}
	service := core.NewService(
		taskRepo,
		taskRepo,
		gitclient.NewRepository(runner),
		tmuxclient.NewRepository(runner),
		map[string]core.ProviderClient{
			"codex":  codexclient.NewRepository(runner, cfg.Codex),
			"claude": claudeclient.NewRepository(runner, cfg.Claude),
		},
		agentconfigfs.NewLoader(),
		workspacefs.NewSeeder(),
		codexhooksfs.NewBootstrapper(
			cfg.SQLite.Path,
			"http://"+cfg.Hooks.ListenAddr+"/hook",
			agentExec,
			detectAgentSourceRoot(),
		),
		cfg.Service,
	)

	return cli.Dependencies{
		Service:            service,
		HookIngestor:       taskRepo,
		ObserverProcess:    observer.NewProcessManager(observer.ProcessConfig{SocketPath: cfg.Observer.SocketPath, ExecPath: agentExec}),
		HookListenAddr:     cfg.Hooks.ListenAddr,
		ObserverSocketPath: cfg.Observer.SocketPath,
		Stdout:             os.Stdout,
		Stderr:             os.Stderr,
		DefaultProvider:    cfg.Service.Provider,
	}, nil
}

func detectAgentSourceRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok || file == "" {
		return ""
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
