package main

import (
	"fmt"
	"os"
	"time"

	"agent/internal/adapters/handler/cli"
	agentconfigrepo "agent/internal/adapters/repository/agentconfig"
	clauderepo "agent/internal/adapters/repository/claude"
	codexrepo "agent/internal/adapters/repository/codex"
	gitrepo "agent/internal/adapters/repository/git"
	sqliterepo "agent/internal/adapters/repository/sqlite"
	tmuxrepo "agent/internal/adapters/repository/tmux"
	workspacerepo "agent/internal/adapters/repository/workspace"
	"agent/internal/core"
	"agent/internal/infrastructure"
	"agent/internal/pkg/execx"
	"agent/internal/pkg/timeutil"
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
	runtimeMonitor := tmuxrepo.NewRuntimeMonitor()

	taskRepo, err := sqliterepo.NewRepository(cfg.SQLite)
	if err != nil {
		return cli.Dependencies{}, err
	}

	service := core.NewService(
		taskRepo,
		gitrepo.NewRepository(runner),
		tmuxrepo.NewRepository(runner),
		map[string]core.ProviderRepository{
			"codex":  codexrepo.NewRepository(runner, cfg.Codex),
			"claude": clauderepo.NewRepository(runner, cfg.Claude),
		},
		runtimeMonitor,
		map[string]core.RuntimeStateDetector{
			"codex":  codexrepo.NewRuntimeDetector(2 * time.Second),
			"claude": clauderepo.NewRuntimeDetector(2 * time.Second),
		},
		agentconfigrepo.NewRepository(),
		workspacerepo.NewRepository(),
		timeutil.RealClock{},
		cfg.Service,
	)

	return cli.Dependencies{
		Service: service,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}, nil
}
