package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"rig/internal/adapters/taskdaemon"
	"rig/internal/core"
	"rig/internal/infrastructure"
	"rig/internal/pkg/subprocess"

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
	Provider:         core.AgentProviderCodex,
	PrepareWorkspace: false,
}

var debugCodexAgentConfig = codexagent.Config{
	Binary: string(core.AgentProviderCodex),
}

var debugCodexHookForwarding = codexagent.HookForwardingConfig{
	RigBinaryPath: "/Users/ebon/personal_software/rig/local/bin/rig",
	SourceRoot:    "/Users/ebon/personal_software/rig",
}

var debugTaskDaemon = debugTaskDaemonConfig{
	ModeEnvKey:      "RIG_DEBUG_MODE",
	ModeEnvValue:    "task-daemon",
	StatusWaitAfter: 0,
}

type debugCreateConfig struct {
	Cwd              string
	Prompt           string
	Provider         core.AgentProvider
	PrepareWorkspace bool
}

type debugTaskDaemonConfig struct {
	ModeEnvKey      string
	ModeEnvValue    string
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

	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	runner := subprocess.ExecRunner{}
	codexCfg := debugCodexAgentConfig
	codexCfg.Binary = cfg.Codex.Binary
	debugHookForwarding := debugCodexHookForwarding
	debugHookForwarding.RigBinaryPath = execPath

	agents := map[core.AgentProvider]core.AgentClient{
		core.AgentProviderCodex: codexagent.New(runner, codexCfg, debugHookForwarding),
	}

	service := core.NewTaskService(core.TaskServiceDependencies{
		Tasks:                taskStore,
		GitWorktree:          gitworktree.New(runner),
		TmuxSession:          tmuxsession.New(runner),
		Agents:               agents,
		Workspace:            repositoryworkspace.New(),
		EnableWorkspaceSetup: debugCreate.PrepareWorkspace,
		DefaultProvider:      cfg.Provider,
	})

	fmt.Println("rig debug starting with config")
	if os.Getenv(debugTaskDaemon.ModeEnvKey) == debugTaskDaemon.ModeEnvValue {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		adapter := taskdaemon.New(cfg.TaskDaemon)
		if err := adapter.Serve(
			ctx,
			service,
			debugDaemonHookRoutes(service),
			cancel,
		); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	adapter := taskdaemon.New(taskdaemon.Config{
		SocketPath:     cfg.TaskDaemon.SocketPath,
		HookListenAddr: cfg.TaskDaemon.HookListenAddr,
		ExecPath:       execPath,
		Env: []string{
			debugTaskDaemon.ModeEnvKey + "=" + debugTaskDaemon.ModeEnvValue,
		},
	})
	if err := adapter.Restart(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	statusCtx, cancelStatus := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelStatus()
	frontend := adapter.Frontend()

	task, err := frontend.CreateTask(context.Background(), core.CreateTaskInput{
		Cwd:      strings.TrimSpace(debugCreate.Cwd),
		Prompt:   strings.TrimSpace(debugCreate.Prompt),
		Provider: debugCreate.Provider,
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

	latest, err := frontend.LatestTaskStatus(context.Background(), task.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	updates, err := frontend.SubscribeTaskStatus(statusCtx, task.ID)
	if err != nil {
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
	if latest != nil {
		if _, err := fmt.Fprintf(
			os.Stdout,
			"task_status task_id=%s provider=%s phase=%s raw_event=%s observed_at=%s\n",
			latest.TaskID,
			latest.Provider,
			latest.Phase,
			latest.RawEventName,
			latest.ObservedAt.Format(time.RFC3339),
		); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	var statusDeadline <-chan time.Time
	if debugTaskDaemon.StatusWaitAfter > 0 {
		timer := time.NewTimer(debugTaskDaemon.StatusWaitAfter)
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
				debugTaskDaemon.StatusWaitAfter,
			); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}
}

func debugDaemonHookRoutes(service core.TaskService) []core.TaskDaemonHookRoute {
	codexHooks := codexagent.NewHookHTTPHandler(service, nil)

	return []core.TaskDaemonHookRoute{
		{Path: "/hook", Handler: codexHooks},
		{Path: "/codex-hook", Handler: codexHooks},
	}
}
