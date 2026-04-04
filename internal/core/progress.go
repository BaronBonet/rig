package core

type TaskProgressStep string

const (
	TaskProgressNaming           TaskProgressStep = "naming"
	TaskProgressNameSelected     TaskProgressStep = "name_selected"
	TaskProgressWorktreeCreating TaskProgressStep = "worktree_creating"
	TaskProgressWorkspaceSeeding TaskProgressStep = "workspace_seeding"
	TaskProgressWorkspaceSeeded  TaskProgressStep = "workspace_seeded"
	TaskProgressTmuxStarting     TaskProgressStep = "tmux_starting"
	TaskProgressCodexLaunching   TaskProgressStep = "codex_launching"
	TaskProgressTaskCreated      TaskProgressStep = "task_created"
	TaskProgressSessionOpening   TaskProgressStep = "session_opening"
)

type TaskProgress struct {
	Task    *Task
	Step    TaskProgressStep
	Message string
}

type CreateTaskOptions struct {
	OpenSession bool
}
