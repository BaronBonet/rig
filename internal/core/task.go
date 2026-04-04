package core

import "time"

type Task struct {
	ID                 string
	Prompt             string
	DisplayName        string
	Slug               string
	RepoRoot           string
	RepoName           string
	BaseBranch         string
	BranchName         string
	WorktreePath       string
	TmuxSession        string
	AgentWindowName    string
	EditorWindowName   string
	Provider           string
	Status             TaskStatus
	WorktreeExists     bool
	BranchExists       bool
	SessionExists      bool
	AgentWindowExists  bool
	EditorWindowExists bool
	LastError          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LastReconciledAt   time.Time
}
