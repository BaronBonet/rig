package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskService_PullRequestStatusFetchesLatestBranchStatus(t *testing.T) {
	h := newTestTaskService(t)
	h.pullRequests.statusSequence = []*PRStatus{
		{State: PRStateOpen, Number: 42},
		{State: PRStateMerged, Number: 42},
	}

	status, err := h.service.PullRequestStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, &PRStatus{State: PRStateOpen, Number: 42}, status)

	status, err = h.service.PullRequestStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, &PRStatus{State: PRStateMerged, Number: 42}, status)
	require.Equal(t, 2, h.pullRequests.checkStatusCalls)
}

func TestTaskService_PullRequestStatusReturnsNoneWhenNoClientConfigured(t *testing.T) {
	h := newTestTaskService(t)
	h.service = NewTaskService(TaskServiceDependencies{
		Tasks:           h.taskRepoMock,
		GitWorktree:     h.repoClientMock,
		TmuxSession:     h.sessionClientMock,
		PullRequests:    nil,
		Providers:       map[Provider]ProviderClient{ProviderCodex: h.providerClientMock},
		Workspace:       h.workspaceMock,
		DefaultProvider: ProviderCodex,
	})

	status, err := h.service.PullRequestStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, &PRStatus{State: PRStateNone}, status)
}
