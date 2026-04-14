package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	claudeclient "rig/internal/adapters/client/claude"
	codexclient "rig/internal/adapters/client/codex"
	gitclient "rig/internal/adapters/client/git"
	ghclient "rig/internal/adapters/client/github"
	tmuxclient "rig/internal/adapters/client/tmux"
	agentconfigfs "rig/internal/adapters/filesystem/agentconfig"
	codexhooksfs "rig/internal/adapters/filesystem/codexhooks"
	sessionusagefs "rig/internal/adapters/filesystem/sessionusage"
	setupscriptfs "rig/internal/adapters/filesystem/setupscript"
	workspacefs "rig/internal/adapters/filesystem/workspace"
	"rig/internal/adapters/handler/cli"
	observer "rig/internal/adapters/observability/observer"
	sqliterepo "rig/internal/adapters/repository/sqlite"
	"rig/internal/core"
	"rig/internal/infrastructure"
	"rig/internal/pkg/execx"
)

var version = "dev"

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
	cwd, err := os.Getwd()
	if err != nil {
		return cli.Dependencies{}, fmt.Errorf("get working directory: %w", err)
	}

	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		return cli.Dependencies{}, err
	}

	runner := execx.ExecRunner{}
	providers := map[string]core.ProviderClient{
		"codex":  codexclient.NewRepository(runner, cfg.Codex),
		"claude": claudeclient.NewRepository(runner, cfg.Claude),
	}

	taskRepo, err := sqliterepo.NewRepository(cfg.SQLite)
	if err != nil {
		return cli.Dependencies{}, err
	}

	agentExec, err := os.Executable()
	if err != nil {
		return cli.Dependencies{}, err
	}
	observerFingerprint, err := binaryFingerprint(agentExec)
	if err != nil {
		return cli.Dependencies{}, err
	}
	service := core.NewService(
		taskRepo,
		taskRepo,
		taskRepo,
		gitclient.NewRepository(runner),
		tmuxclient.NewRepository(runner),
		providers,
		agentconfigfs.NewLoader(),
		workspacefs.NewSeeder(),
		codexhooksfs.NewBootstrapper(
			agentExec,
			detectAgentSourceRoot(),
		),
		setupscriptfs.NewRunner(),
		cfg.Service,
	)
	service.SetSessionUsageReader(sessionusagefs.NewRepository())
	service.SetPRStatusChecker(ghclient.NewPRStatusChecker(runner))
	observerWatcher := observer.NewTMuxWatcher(observer.TMuxWatcherConfig{
		Tasks:     taskRepo,
		Monitor:   tmuxclient.NewRuntimeMonitor(),
		Repo:      taskRepo,
		Hooks:     taskRepo,
		Providers: providers,
	})

	return cli.Dependencies{
		Service:      service,
		HookIngestor: taskRepo,
		ObserverProcess: observer.NewProcessManager(observer.ProcessConfig{
			SocketPath:          cfg.Observer.SocketPath,
			ExecPath:            agentExec,
			ExpectedFingerprint: observerFingerprint,
		}),
		ObserverWatcher:     observerWatcher,
		HookListenAddr:      cfg.Hooks.ListenAddr,
		ObserverSocketPath:  cfg.Observer.SocketPath,
		ObserverFingerprint: observerFingerprint,
		Stdout:              os.Stdout,
		Stderr:              os.Stderr,
		Cwd:                 cwd,
		RepoRoot:            detectRepoRoot(cwd),
		DefaultProvider:     cfg.Service.Provider,
		Version:             version,
	}, nil
}

func detectAgentSourceRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok || file == "" {
		return ""
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func detectRepoRoot(cwd string) string {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func binaryFingerprint(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat agent executable: %w", err)
	}

	return strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UTC().UnixNano(), 10), nil
}
