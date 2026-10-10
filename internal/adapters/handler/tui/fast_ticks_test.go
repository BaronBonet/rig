package tui

import "time"

// Tests execute batched commands synchronously, so the background activity
// refresh tick and the order's settle deadline must not block them at their
// production intervals.
func init() {
	activityRefreshInterval = time.Millisecond
	orderSettleDeadline = time.Millisecond
}
