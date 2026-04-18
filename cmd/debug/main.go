package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"rig/internal/core"
	"rig/internal/infrastructure"
	"rig/internal/pkg/execx"
	"runtime"
	"strings"

	claudeclient "rig/internal/adapters/client/claude"
	claudeagent "rig/internal/adapters/client/claudeagent"
	codexclient "rig/internal/adapters/client/codex"
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
	Provider:         "codex",
	PrepareWorkspace: false,
}

// Set this when you want codex hook forwarding in seeded worktrees to call the
// real rig binary instead of the debug executable.
var debugRigBinaryPath = ""

type debugCreateConfig struct {
	Cwd              string
	Prompt           string
	Provider         string
	PrepareWorkspace bool
}

func main() {
	if strings.TrimSpace(debugRigBinaryPath) != "" {
		debugRigBinaryPath = strings.TrimSpace(debugRigBinaryPath)
	}
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

	taskRepo, err := tasksqlite.NewRepository(sqliterepo.Config{Path: cfg.SQLitePath})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	runner := execx.ExecRunner{}
	agentExecPath := debugRigBinaryPath
	if agentExecPath == "" {
		agentExecPath, err = os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	sourceRoot := ""
	_, file, _, ok := runtime.Caller(0)
	if ok && file != "" {
		sourceRoot = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	}

	agents := map[string]core.AgentClient{
		"codex": codexagent.NewRepository(runner, codexclient.Config{
			Binary:        cfg.CodexBinary,
			RigBinaryPath: agentExecPath,
			SourceRoot:    sourceRoot,
		}),
		"claude": claudeagent.NewRepository(runner, claudeclient.Config{
			Binary:         cfg.ClaudeBinary,
			HookListenAddr: cfg.HookListenAddr,
		}),
	}

	var preparer core.WorkspacePreparer
	if debugCreate.PrepareWorkspace {
		preparer = repositoryworkspace.NewPreparer()
	}

	service := core.NewTaskService(core.TaskServiceDependencies{
		Tasks:           taskRepo,
		GitWorktree:     gitworktree.NewRepository(runner),
		TmuxSession:     tmuxsession.NewRepository(runner),
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
