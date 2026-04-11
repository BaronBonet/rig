package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type stubPRStatusChecker func(context.Context, string, string) (*PRStatus, error)

func (s stubPRStatusChecker) IsAvailable(_ context.Context) error { return nil }

func (s stubPRStatusChecker) CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error) {
	return s(ctx, repoRoot, branchName)
}

func TestService_GetPRStatus_FetchesAndCaches(t *testing.T) {
	h := newTestService(t)
	prChecker := NewMockPRStatusChecker(t)
	h.service.SetPRStatusChecker(prChecker)

	prChecker.EXPECT().
		CheckPRStatus(mock.Anything, "/tmp/repo", "feat/auth").
		Return(&PRStatus{State: PRStateOpen, Number: 42}, nil).
		Once()

	status1, err := h.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, PRStateOpen, status1.State)
	require.Equal(t, 42, status1.Number)

	// Second call should use cache — mock would fail if called again.
	status2, err := h.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, PRStateOpen, status2.State)
}

func TestService_GetPRStatus_RefetchesAfterTTL(t *testing.T) {
	h := newTestService(t)
	prChecker := NewMockPRStatusChecker(t)
	h.service.SetPRStatusChecker(prChecker)
	h.service.prCacheTTL = 10 * time.Millisecond

	prChecker.EXPECT().
		CheckPRStatus(mock.Anything, "/tmp/repo", "feat/auth").
		Return(&PRStatus{State: PRStateOpen, Number: 42}, nil).
		Once()
	prChecker.EXPECT().
		CheckPRStatus(mock.Anything, "/tmp/repo", "feat/auth").
		Return(&PRStatus{State: PRStateMerged, Number: 42}, nil).
		Once()

	_, err := h.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)

	time.Sleep(15 * time.Millisecond)

	status, err := h.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, PRStateMerged, status.State)
}

func TestService_GetPRStatus_ReturnsNoneWhenNoChecker(t *testing.T) {
	h := newTestService(t)
	// Don't set prChecker — it defaults to nil.

	status, err := h.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, PRStateNone, status.State)
}

func TestService_GetPRStatus_DoesNotPanicWhenCacheInvalidatedDuringFetch(t *testing.T) {
	h := newTestService(t)
	h.service.SetPRStatusChecker(stubPRStatusChecker(func(context.Context, string, string) (*PRStatus, error) {
		h.service.InvalidatePRCache()
		return &PRStatus{State: PRStateOpen, Number: 42}, nil
	}))

	require.NotPanics(t, func() {
		status, err := h.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
		require.NoError(t, err)
		require.Equal(t, PRStateOpen, status.State)
		require.Equal(t, 42, status.Number)
	})
}
