package core

import (
	"context"
	"time"
)

type RuntimeState string

const (
	RuntimeStateNone       RuntimeState = ""
	RuntimeStateRunning    RuntimeState = "running"
	RuntimeStateNeedsInput RuntimeState = "needs_input"
	RuntimeStateFinished   RuntimeState = "finished"
)

type RuntimeSnapshot struct {
	SessionName       string
	WindowName        string
	PaneID            string
	HadCodexBinding   bool
	ForegroundCommand string
	Content           string
	ObservedAt        time.Time
	LastOutputAt      time.Time
}

type RuntimeMonitor interface {
	Snapshot(ctx context.Context, task *Task) (RuntimeSnapshot, error)
	Close() error
}

type RuntimeStateDetector interface {
	Detect(snapshot RuntimeSnapshot) RuntimeState
}
