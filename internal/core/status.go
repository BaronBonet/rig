package core

type TaskStatus string

const (
	TaskStatusCreating TaskStatus = "creating"
	TaskStatusReady    TaskStatus = "ready"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusBroken   TaskStatus = "broken"
)

func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusBroken
}
