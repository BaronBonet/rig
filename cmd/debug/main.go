package main

import (
	"context"
	"fmt"
	"os"
	"rig/internal/core"
	"rig/internal/infrastructure"
	"rig/internal/pkg/execx"
	"strings"

	claudeclient "rig/internal/adapters/client/claude"
	claudeagent "rig/internal/adapters/client/claudeagent"
	codexagent "rig/internal/adapters/client/codexagent"
	gitworktree "rig/internal/adapters/client/gitworktree"
	tmuxsession "rig/internal/adapters/client/tmuxsession"
	sqliterepo "rig/internal/adapters/repository/sqlite"
	tasksqlite "rig/internal/adapters/repository/tasksqlite"
	repositoryworkspace "rig/internal/adapters/repository/workspace"
)

// Edit these values directly when you want to debug the create-task flow.
var debugCreate = debugCreateConfig{
	Cwd:              "/Users/ebon/personal_software/rig",
	Prompt:           "test creating a rig test",
	Provider:         string(core.AgentProviderCodex),
	PrepareWorkspace: false,
}

var debugCodexAgentConfig = codexagent.Config{
	Binary: string(core.AgentProviderCodex),
}

var debugCodexHookForwarding = codexagent.HookForwardingConfig{
	RigBinaryPath: "/Users/ebon/personal_software/rig/local/bin/rig",
	SourceRoot:    "/Users/ebon/personal_software/rig",
}

type debugCreateConfig struct {
	Cwd              string
	Prompt           string
	Provider         string
	PrepareWorkspace bool
}

func main() {
	if strings.TrimSpace(debugCreate.Cwd) == "" {
		fmt.Fprintln(os.Stderr, "set debugCreate.Cwd in cmd/debug/main.go before running")
		os.Exit(1)
	}
	if strings.TrimSpace(debugCreate.Prompt) == "" {
		fmt.Fprintln(os.Stderr, "set debugCreate.Prompt in cmd/debug/main.go before running")
		os.Exit(1)
	}

	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	taskRepo, err := tasksqlite.New(sqliterepo.Config{Path: cfg.SQLite.Path})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	runner := execx.ExecRunner{}
	codexCfg := debugCodexAgentConfig
	codexCfg.Binary = cfg.Codex.Binary

	agents := map[string]core.AgentClient{
		string(core.AgentProviderCodex): codexagent.New(runner, codexCfg, debugCodexHookForwarding),
		string(core.AgentProviderClaude): claudeagent.New(runner, claudeclient.Config{
			Binary:         cfg.Claude.Binary,
			HookListenAddr: cfg.Claude.HookListenAddr,
		}),
	}

	var preparer core.WorkspacePreparer
	if debugCreate.PrepareWorkspace {
		preparer = repositoryworkspace.New()
	}

	service := core.NewTaskService(core.TaskServiceDependencies{
		Tasks:           taskRepo,
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
			"status=%s\n"+
			"branch=%s\n"+
			"worktree=%s\n"+
			"tmux_session=%s\n",
		task.ID,
		task.DisplayName,
		task.Provider,
		task.Status,
		task.BranchName,
		task.WorktreePath,
		task.TmuxSession,
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
