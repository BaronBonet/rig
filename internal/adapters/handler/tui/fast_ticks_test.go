package tui

import "time"

// Tests execute batched commands synchronously, so the background activity
// refresh and spinner ticks must not block them at their production intervals.
func init() {
	activityRefreshInterval = time.Millisecond
	spinnerTickInterval = time.Millisecond
}
