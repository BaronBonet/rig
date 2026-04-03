package core

type TaskStatus string

const (
	TaskStatusCreating TaskStatus = "creating"
	TaskStatusReady    TaskStatus = "ready"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusBroken   TaskStatus = "broken"
	TaskStatusCleaned  TaskStatus = "cleaned"
)

func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusBroken || s == TaskStatusCleaned
}
