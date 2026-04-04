package core

import "time"

type Task struct {
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	LastReconciledAt   time.Time  `json:"last_reconciled_at"`
	ID                 string     `json:"id"`
	Prompt             string     `json:"prompt"`
	DisplayName        string     `json:"display_name"`
	Slug               string     `json:"slug"`
	RepoRoot           string     `json:"repo_root"`
	RepoName           string     `json:"repo_name"`
	BaseBranch         string     `json:"base_branch"`
	BranchName         string     `json:"branch_name"`
	WorktreePath       string     `json:"worktree_path"`
	TmuxSession        string     `json:"tmux_session"`
	AgentWindowName    string     `json:"agent_window_name"`
	EditorWindowName   string     `json:"editor_window_name"`
	Provider           string     `json:"provider"`
	Status             TaskStatus `json:"status"`
	LastError          string     `json:"last_error"`
	WorktreeExists     bool       `json:"worktree_exists"`
	BranchExists       bool       `json:"branch_exists"`
	SessionExists      bool       `json:"session_exists"`
	AgentWindowExists  bool       `json:"agent_window_exists"`
	EditorWindowExists bool       `json:"editor_window_exists"`
}
