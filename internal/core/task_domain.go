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
	CreatedAt time.Time
	UpdatedAt time.Time
	ID        string
	// Slug is the stable workspace identifier derived once at task creation from
	// DisplayName and then persisted so branch/worktree/session naming remains
	// stable even if display names collide or later change.
	Slug         string
	Prompt       string
	DisplayName  string
	RepoRoot     string
	RepoName     string
	BranchName   string
	WorktreePath string
	TmuxSession  string
	Provider     AgentProvider
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

// TaskStatusPhase is the small application-facing runtime status model used by
// the first hook-driven status stream slice.
type TaskStatusPhase string

const (
	TaskStatusPhaseStarting        TaskStatusPhase = "starting"
	TaskStatusPhaseWorking         TaskStatusPhase = "working"
	TaskStatusPhaseWaitingForInput TaskStatusPhase = "waiting_for_input"
)

// TaskStatusUpdate is the live status message published by the observer
// process. It is intentionally separate from the durable Task record.
type TaskStatusUpdate struct {
	TaskID       string
	Provider     AgentProvider
	Phase        TaskStatusPhase
	RawEventName string
	ObservedAt   time.Time
}

// AgentProvider identifies the supported interactive coding agent backing a
// task.
type AgentProvider string

const (
	AgentProviderCodex AgentProvider = "codex"
)

// TaskSessionLaunchSpec is the handoff from an agent client to the tmux
// session client for starting an interactive agent session.
//
// This is not a domain object. It is an application-facing integration DTO that
// describes how the tmux adapter should start the provider's CLI.
type TaskSessionLaunchSpec struct {
	// Command is the argv launched in the task's agent tmux window, for example
	// []string{"codex"}.
	Command []string
	// ReadyMarker is the terminal prompt marker emitted by the agent when it is
	// ready to receive interactive input. The tmux session client waits for this
	// marker before typing PrefillInput into the window.
	ReadyMarker string
	// PrefillInput is the text typed into the interactive agent after the
	// command has started and the ReadyMarker has appeared. For create-task,
	// this is the drafted task prompt that is placed into the fresh agent
	// session without being submitted.
	PrefillInput []string
}
