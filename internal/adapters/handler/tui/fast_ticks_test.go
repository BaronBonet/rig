package tui

import "time"

// Tests execute batched commands synchronously, so the background activity
// refresh tick must not block them at its production interval.
func init() {
	activityRefreshInterval = time.Millisecond
}
