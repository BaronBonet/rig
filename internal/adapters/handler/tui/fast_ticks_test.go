package tui

import "time"

// Tests execute batched commands synchronously, so the background activity
// and pull request refreshes and the spinner ticks must not block them at
// their production intervals.
func init() {
	activityRefreshInterval = time.Millisecond
	pullRequestRefreshInterval = time.Millisecond
	taskListRefreshInterval = time.Millisecond
	spinnerTickInterval = time.Millisecond
	noticeDuration = time.Millisecond
}
