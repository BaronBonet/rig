package core

import "time"

type TmpTask struct {
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
	LastReconciledAt      time.Time    `json:"last_reconciled_at"`
	RuntimeStateUpdatedAt time.Time    `json:"runtime_state_updated_at"`
	ID                    string       `json:"id"`
	Prompt                string       `json:"prompt"`
	DisplayName           string       `json:"display_name"`
	Slug                  string       `json:"slug"`
	RepoRoot              string       `json:"repo_root"`
	RepoName              string       `json:"repo_name"`
	BaseBranch            string       `json:"base_branch"`
	BranchName            string       `json:"branch_name"`
	WorktreePath          string       `json:"worktree_path"`
	TmuxSession           string       `json:"tmux_session"`
	AgentWindowName       string       `json:"agent_window_name"`
	EditorWindowName      string       `json:"editor_window_name"`
	Provider              string       `json:"provider"`
	Status                TaskStatus   `json:"status"`
	RuntimeState          RuntimeState `json:"runtime_state"`
	LastError             string       `json:"last_error"`
	WorktreeExists        bool         `json:"worktree_exists"`
	BranchExists          bool         `json:"branch_exists"`
	SessionExists         bool         `json:"session_exists"`
	AgentWindowExists     bool         `json:"agent_window_exists"`
	EditorWindowExists    bool         `json:"editor_window_exists"`
}

type RepoContext struct {
	Root       string
	Name       string
	BaseBranch string
}

type TaskSuggestion struct {
	Name       string `json:"name"`
	BranchType string `json:"branch_type"`
}

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
