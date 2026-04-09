package observer

import "agent/internal/core"

type StatusInput struct {
	TaskStatus    core.TaskStatus
	RuntimeState  core.RuntimeState
	ProcessAlive  bool
	ActiveCommand bool
}

func DeriveDisplayStatus(in StatusInput) core.DisplayState {
	switch {
	case in.TaskStatus.IsTerminal():
		return core.DisplayState{Primary: core.DisplayStatusFinished}
	case in.RuntimeState == core.RuntimeStateNeedsInput:
		return core.DisplayState{Primary: core.DisplayStatusNeedsInput}
	case in.ProcessAlive && in.ActiveCommand:
		return core.DisplayState{Primary: core.DisplayStatusWorking, Activity: core.DisplayActivityCommand}
	case in.ProcessAlive:
		return core.DisplayState{Primary: core.DisplayStatusWorking}
	default:
		return core.DisplayState{Primary: core.DisplayStatusDisconnected}
	}
}
