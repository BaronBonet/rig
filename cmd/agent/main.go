package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	claudeclient "agent/internal/adapters/client/claude"
	codexclient "agent/internal/adapters/client/codex"
	gitclient "agent/internal/adapters/client/git"
	tmuxclient "agent/internal/adapters/client/tmux"
	agentconfigfs "agent/internal/adapters/filesystem/agentconfig"
	codexhooksfs "agent/internal/adapters/filesystem/codexhooks"
	workspacefs "agent/internal/adapters/filesystem/workspace"
	"agent/internal/adapters/handler/cli"
	hookhttp "agent/internal/adapters/observability/codexhooks"
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

	agentExec, _ := os.Executable()
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
		Service:         service,
		HookIngestor:    taskRepo,
		StartHookServer: func() (func(), error) { return startHookServer(taskRepo, cfg.Hooks.ListenAddr) },
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		DefaultProvider: cfg.Service.Provider,
	}, nil
}

func startHookServer(repo core.HookEventIngestor, listenAddr string) (func(), error) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if isAddrInUse(err) {
			return func() {}, nil
		}
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle("/hook", hookhttp.NewHTTPHandler(repo, time.Now))
	server := &http.Server{Handler: mux}

	go func() {
		_ = server.Serve(listener)
	}()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}, nil
}

func isAddrInUse(err error) bool {
	return errors.Is(err, os.ErrExist) || strings.Contains(err.Error(), "address already in use")
}

func detectAgentSourceRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok || file == "" {
		return ""
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
