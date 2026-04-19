package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	statusstream "rig/internal/adapters/observability/statusstream"
	"rig/internal/core"
	"rig/internal/infrastructure"
	"rig/internal/pkg/execx"
	"strings"
	"syscall"
	"time"

	claudeclient "rig/internal/adapters/client/claude"
	claudeagent "rig/internal/adapters/client/claudeagent"
	codexagent "rig/internal/adapters/client/codexagent"
	gitworktree "rig/internal/adapters/client/gitworktree"
	tmuxsession "rig/internal/adapters/client/tmuxsession"
	tasksqlite "rig/internal/adapters/repository/tasksqlite"
	repositoryworkspace "rig/internal/adapters/repository/workspace"
)

// Edit these values directly when you want to debug the create-task flow.
var debugCreate = debugCreateConfig{
	Cwd:              "/Users/ebon/personal_software/rig",
	Prompt:           "print hi 10 times then stop",
	Provider:         string(core.AgentProviderCodex),
	PrepareWorkspace: true,
}

var debugCodexAgentConfig = codexagent.Config{
	Binary: string(core.AgentProviderCodex),
}

var debugCodexHookForwarding = codexagent.HookForwardingConfig{
	RigBinaryPath: "/Users/ebon/personal_software/rig/local/bin/rig",
	SourceRoot:    "/Users/ebon/personal_software/rig",
}

var debugStatusObserver = debugStatusObserverConfig{
	ModeEnvKey:      "RIG_DEBUG_MODE",
	ModeEnvValue:    "status-observer",
	HookListenAddr:  "127.0.0.1:4123",
	StatusWaitAfter: 0,
}

type debugCreateConfig struct {
	Cwd              string
	Prompt           string
	Provider         string
	PrepareWorkspace bool
}

type debugStatusObserverConfig struct {
	ModeEnvKey      string
	ModeEnvValue    string
	HookListenAddr  string
	StatusWaitAfter time.Duration
}

func main() {
	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	taskStore, err := tasksqlite.New(tasksqlite.Config{Path: cfg.TaskSQLite.Path})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Println("rig debug starting with config")
	if os.Getenv(debugStatusObserver.ModeEnvKey) == debugStatusObserver.ModeEnvValue {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		if err := statusstream.Serve(ctx, statusstream.ServerConfig{
			SocketPath:     cfg.Observer.SocketPath,
			HookListenAddr: debugStatusObserver.HookListenAddr,
			HookIngestor:   newDebugHookIngestor(taskStore),
			Hub:            statusstream.NewHub(),
			Stop:           cancel,
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if strings.TrimSpace(debugCreate.Cwd) == "" {
		fmt.Fprintln(os.Stderr, "set debugCreate.Cwd in cmd/debug/main.go before running")
		os.Exit(1)
	}
	if strings.TrimSpace(debugCreate.Prompt) == "" {
		fmt.Fprintln(os.Stderr, "set debugCreate.Prompt in cmd/debug/main.go before running")
		os.Exit(1)
	}
	if !debugCreate.PrepareWorkspace {
		fmt.Fprintln(os.Stderr, "set debugCreate.PrepareWorkspace=true to debug hook-driven status streaming")
		os.Exit(1)
	}

	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	manager := statusstream.NewProcessManager(statusstream.ProcessConfig{
		SocketPath: cfg.Observer.SocketPath,
		ExecPath:   execPath,
		Env: []string{
			debugStatusObserver.ModeEnvKey + "=" + debugStatusObserver.ModeEnvValue,
		},
	})
	if err := manager.Restart(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	statusCtx, cancelStatus := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelStatus()

	updates, cleanup, err := statusstream.Subscribe(statusCtx, cfg.Observer.SocketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer cleanup()

	runner := execx.ExecRunner{}
	codexCfg := debugCodexAgentConfig
	codexCfg.Binary = cfg.Codex.Binary
	debugHookForwarding := debugCodexHookForwarding
	debugHookForwarding.RigBinaryPath = execPath

	agents := map[string]core.AgentClient{
		string(core.AgentProviderCodex): codexagent.New(runner, codexCfg, debugHookForwarding),
		string(core.AgentProviderClaude): claudeagent.New(runner, claudeclient.Config{
			Binary:         cfg.Claude.Binary,
			HookListenAddr: debugStatusObserver.HookListenAddr,
		}),
	}

	var preparer core.WorkspacePreparer
	if debugCreate.PrepareWorkspace {
		preparer = repositoryworkspace.New()
	}

	service := core.NewTaskService(core.TaskServiceDependencies{
		Tasks:           taskStore,
		GitWorktree:     gitworktree.New(runner),
		TmuxSession:     tmuxsession.New(runner),
		Agents:          agents,
		Preparer:        preparer,
		DefaultProvider: cfg.Provider,
	})

	task, err := service.CreateTask(context.Background(), core.CreateTaskInput{
		Cwd:      strings.TrimSpace(debugCreate.Cwd),
		Prompt:   strings.TrimSpace(debugCreate.Prompt),
		Provider: strings.TrimSpace(debugCreate.Provider),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if _, err := fmt.Fprintf(
		os.Stdout,
		"task_id=%s\n"+
			"display_name=%s\n"+
			"provider=%s\n"+
			"branch=%s\n"+
			"worktree=%s\n"+
			"tmux_session=%s\n",
		task.ID,
		task.DisplayName,
		task.Provider,
		task.BranchName,
		task.WorktreePath,
		task.TmuxSession,
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := fmt.Fprintf(
		os.Stdout,
		"task_status_stream=subscribed\n"+
			"next_step=submit the drafted prompt manually in tmux session %s to verify status streaming\n",
		task.TmuxSession,
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var statusDeadline <-chan time.Time
	if debugStatusObserver.StatusWaitAfter > 0 {
		timer := time.NewTimer(debugStatusObserver.StatusWaitAfter)
		defer timer.Stop()
		statusDeadline = timer.C
	}

	for {
		select {
		case <-statusCtx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				fmt.Fprintln(os.Stderr, "status stream closed unexpectedly")
				os.Exit(1)
			}
			if _, err := fmt.Fprintf(
				os.Stdout,
				"task_status task_id=%s provider=%s phase=%s raw_event=%s observed_at=%s\n",
				update.TaskID,
				update.Provider,
				update.Phase,
				update.RawEventName,
				update.ObservedAt.Format(time.RFC3339),
			); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		case <-statusDeadline:
			if _, err := fmt.Fprintf(
				os.Stdout,
				"status_wait_complete=no updates observed for task %s within %s\n",
				task.ID,
				debugStatusObserver.StatusWaitAfter,
			); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}
}
