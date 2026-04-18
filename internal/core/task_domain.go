package core

import (
	"os"
	"time"
)

// Task is the durable business record for a task.
//
// It intentionally excludes live runtime observations and derived existence
// checks. Those belong in separate runtime/read-side types rather than on the
// core task record itself.
type Task struct {
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ID           string
	Prompt       string
	DisplayName  string
	RepoRoot     string
	RepoName     string
	BranchName   string
	WorktreePath string
	TmuxSession  string
	Provider     AgentProvider
	Status       TaskStatus
}

type RepoContext struct {
	// Root is the canonical absolute path to the repository root on disk.
	Root       string
	Name       string
	BaseBranch string
}

type TaskSuggestion struct {
	Name       string
	BranchType string
}

type WorkspaceBootstrapSpec struct {
	Files []WorkspaceBootstrapFile
}

type WorkspaceBootstrapFile struct {
	Path     string
	Content  []byte
	FileMode os.FileMode
}

// AgentProvider identifies the supported interactive coding agent backing a
// task.
type AgentProvider string

const (
	AgentProviderCodex  AgentProvider = "codex"
	AgentProviderClaude AgentProvider = "claude"
)

// TaskSessionLaunchSpec is the handoff from an agent client to the tmux
// session client for starting an interactive agent session.
//
// This is not a domain object. It is an application-facing integration DTO that
// describes how the tmux adapter should start the provider's CLI.
type TaskSessionLaunchSpec struct {
	// Command is the argv launched in the task's agent tmux window, for example
	// []string{"codex"} or []string{"claude", "--resume", "<session-id>"}.
	Command []string
	// ReadyMarker is the terminal prompt marker emitted by the agent when it is
	// ready to receive interactive input. The tmux session client waits for this
	// marker before typing InitialInput into the window.
	ReadyMarker string
	// InitialInput is the text sent to the interactive agent after the command
	// has started and the ReadyMarker has appeared. For create-task, this is the
	// task prompt that gets pasted into the fresh agent session.
	InitialInput []string
}
