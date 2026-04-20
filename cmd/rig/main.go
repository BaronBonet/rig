package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	claudeclient "rig/internal/adapters/client/claude"
	claudeagent "rig/internal/adapters/client/claudeagent"
	codexclient "rig/internal/adapters/client/codex"
	codexagent "rig/internal/adapters/client/codexagent"
	gitclient "rig/internal/adapters/client/git"
	ghclient "rig/internal/adapters/client/github"
	gitworktree "rig/internal/adapters/client/gitworktree"
	tmuxclient "rig/internal/adapters/client/tmux"
	tmuxsession "rig/internal/adapters/client/tmuxsession"
	codexhooksfs "rig/internal/adapters/filesystem/codexhooks"
	sessionusagefs "rig/internal/adapters/filesystem/sessionusage"
	"rig/internal/adapters/handler/cli"
	observer "rig/internal/adapters/observability/observer"
	sqliterepo "rig/internal/adapters/repository/sqlite"
	tasksqlite "rig/internal/adapters/repository/tasksqlite"
	repositoryworkspace "rig/internal/adapters/repository/workspace"
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
	agentExec, err := os.Executable()
	if err != nil {
		return cli.Dependencies{}, err
	}
	agentSourceRoot := detectAgentSourceRoot()
	providers := map[string]core.ProviderClient{
		string(core.AgentProviderCodex): codexclient.NewRepository(runner, codexclient.Config{
			Binary: cfg.Codex.Binary,
		}),
		string(core.AgentProviderClaude): claudeclient.NewRepository(runner, claudeclient.Config{
			Binary:         cfg.Claude.Binary,
			HookListenAddr: cfg.Claude.HookListenAddr,
		}),
	}
	agentClients := map[string]core.AgentClient{
		string(core.AgentProviderCodex): codexagent.New(runner, cfg.Codex, codexagent.HookForwardingConfig{
			RigBinaryPath: agentExec,
			SourceRoot:    agentSourceRoot,
		}),
		string(core.AgentProviderClaude): claudeagent.New(runner, claudeclient.Config{
			Binary:         cfg.Claude.Binary,
			HookListenAddr: cfg.Claude.HookListenAddr,
		}),
	}

	taskRepo, err := sqliterepo.NewRepository(sqliterepo.Config{Path: cfg.SQLite.Path})
	if err != nil {
		return cli.Dependencies{}, err
	}
	taskStore, err := tasksqlite.New(tasksqlite.Config{Path: cfg.TaskSQLite.Path})
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
		repositoryworkspace.NewRepoConfigLoader(),
		repositoryworkspace.NewSeeder(),
		codexhooksfs.NewBootstrapper(
			agentExec,
			detectAgentSourceRoot(),
		),
		repositoryworkspace.NewSetupScriptRunner(),
		core.Config{Provider: cfg.Provider},
	)
	service.SetSessionUsageReader(sessionusagefs.NewRepository())
	service.SetPRStatusChecker(ghclient.NewPRStatusChecker(runner))

	taskService := core.NewTaskService(core.TaskServiceDependencies{
		Tasks:                taskStore,
		GitWorktree:          gitworktree.New(runner),
		TmuxSession:          tmuxsession.New(runner),
		Agents:               agentClients,
		Workspace:            repositoryworkspace.New(),
		EnableWorkspaceSetup: true,
		DefaultProvider:      cfg.Provider,
	})
	appService := core.NewAppService(taskService, service)

	observerWatcher := observer.NewTMuxWatcher(observer.TMuxWatcherConfig{
		Tasks:     taskRepo,
		Monitor:   tmuxclient.NewRuntimeMonitor(),
		Repo:      taskRepo,
		Hooks:     taskRepo,
		Providers: providers,
	})

	return cli.Dependencies{
		Service:      appService,
		HookIngestor: taskRepo,
		ObserverProcess: observer.NewProcessManager(observer.ProcessConfig{
			SocketPath:          cfg.TaskDaemon.SocketPath,
			ExecPath:            agentExec,
			ExpectedFingerprint: observerFingerprint,
		}),
		ObserverWatcher:     observerWatcher,
		HookListenAddr:      cfg.TaskDaemon.HookListenAddr,
		ObserverSocketPath:  cfg.TaskDaemon.SocketPath,
		ObserverFingerprint: observerFingerprint,
		Stdout:              os.Stdout,
		Stderr:              os.Stderr,
		Cwd:                 cwd,
		RepoRoot:            detectRepoRoot(cwd),
		DefaultProvider:     string(cfg.Provider),
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
	repo, err := gitclient.NewRepository(execx.ExecRunner{}).DetectRepo(context.Background(), cwd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(repo.Root)
}

func binaryFingerprint(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat agent executable: %w", err)
	}

	return strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UTC().UnixNano(), 10), nil
}
