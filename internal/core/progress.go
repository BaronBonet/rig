package core

type TaskProgressStep string

const (
	TaskProgressNaming           TaskProgressStep = "naming"
	TaskProgressNameSelected     TaskProgressStep = "name_selected"
	TaskProgressWorktreeCreating TaskProgressStep = "worktree_creating"
	TaskProgressTmuxStarting     TaskProgressStep = "tmux_starting"
	TaskProgressCodexLaunching   TaskProgressStep = "codex_launching"
	TaskProgressTaskCreated      TaskProgressStep = "task_created"
	TaskProgressSessionOpening   TaskProgressStep = "session_opening"
)

type TaskProgress struct {
	Step    TaskProgressStep
	Message string
	Task    *Task
}

type CreateTaskOptions struct {
	OpenSession bool
}
