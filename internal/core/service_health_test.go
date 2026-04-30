package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceHealthCheck_AggregatesAdapterHealth(t *testing.T) {
	h := newTestTaskService(t)
	h.taskRepo.healthErr = errors.New("database corrupt")
	h.repoClient.healthErr = nil
	h.sessionClient.healthErr = errors.New("tmux missing")
	h.providerRepo.healthErr = nil
	h.pullRequests.healthErr = errors.New("gh not authenticated")

	checks, err := h.service.HealthCheck(t.Context())

	requireHealthCheck(t, checks, "git", true, "")
	requireHealthCheck(t, checks, "tmux", true, "tmux missing")
	requireHealthCheck(t, checks, "codex", true, "")
	requireHealthCheck(t, checks, "gh", false, "gh not authenticated")
	requireHealthCheck(t, checks, "sqlite", true, "database corrupt")
	require.ErrorContains(t, err, "tmux")
	require.ErrorContains(t, err, "sqlite")
	require.NotErrorIs(t, err, h.pullRequests.healthErr)
}

func requireHealthCheck(t *testing.T, checks []HealthCheck, name string, required bool, errContains string) {
	t.Helper()

	for _, check := range checks {
		if check.Name != name {
			continue
		}
		require.Equal(t, required, check.Required)
		if errContains == "" {
			require.NoError(t, check.Err)
		} else {
			require.ErrorContains(t, check.Err, errContains)
		}
		return
	}

	t.Fatalf("missing health check %q in %#v", name, checks)
}
