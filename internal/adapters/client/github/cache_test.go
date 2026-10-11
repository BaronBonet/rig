package github

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeGh stands in for gh: it answers each branch with its pull requests and
// each repository with its default branch, or fails with err, and counts the
// lookups by repository root and, for branches, branch name.
type fakeGh struct {
	byBranch       map[string][]core.PullRequest
	defaultBranch  map[string]string
	err            error
	listings       map[string]int
	defaultLookups map[string]int
}

// cacheUnderTest is a cache in front of a fakeGh, on a clock the test moves.
type cacheUnderTest struct {
	cache  *pullRequestCache
	gh     *fakeGh
	client *core.MockPullRequestClient
	now    time.Time
}

func newCacheUnderTest(t *testing.T) *cacheUnderTest {
	t.Helper()
	fake := &fakeGh{
		byBranch:       map[string][]core.PullRequest{},
		defaultBranch:  map[string]string{},
		listings:       map[string]int{},
		defaultLookups: map[string]int{},
	}
	client := core.NewMockPullRequestClient(t)
	client.EXPECT().ListBranchPullRequests(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, repoRoot string, branchName string) ([]core.PullRequest, error) {
			fake.listings[repoRoot+":"+branchName]++
			if fake.err != nil {
				return nil, fake.err
			}
			return fake.byBranch[branchName], nil
		},
	).Maybe()
	client.EXPECT().DefaultBranch(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, repoRoot string) (string, error) {
			fake.defaultLookups[repoRoot]++
			if fake.err != nil {
				return "", fake.err
			}
			return fake.defaultBranch[repoRoot], nil
		},
	).Maybe()

	under := &cacheUnderTest{gh: fake, client: client, now: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)}
	under.cache = newPullRequestCache(client, func() time.Time { return under.now })
	return under
}

func (c *cacheUnderTest) after(elapsed time.Duration) {
	c.now = c.now.Add(elapsed)
}

func (c *cacheUnderTest) list(t *testing.T, branchName string) ([]core.PullRequest, error) {
	t.Helper()
	return c.cache.ListBranchPullRequests(t.Context(), "/tmp/repo", branchName)
}

func openPullRequest() []core.PullRequest {
	return []core.PullRequest{{Number: 127, State: core.PRStateOpen}}
}

func TestPullRequestCache_ServesABranchFromTheCacheWithinAMinute(t *testing.T) {
	c := newCacheUnderTest(t)
	c.gh.byBranch["feat/billing"] = openPullRequest()

	first, err := c.list(t, "feat/billing")
	require.NoError(t, err)
	c.gh.byBranch["feat/billing"] = []core.PullRequest{{Number: 127, State: core.PRStateMerged}}
	c.after(59 * time.Second)
	second, err := c.list(t, "feat/billing")
	require.NoError(t, err)

	require.Equal(t, openPullRequest(), first)
	require.Equal(t, first, second)
	require.Equal(t, map[string]int{"/tmp/repo:feat/billing": 1}, c.gh.listings)
}

func TestPullRequestCache_CachesEachRepositorysBranchApart(t *testing.T) {
	c := newCacheUnderTest(t)

	for range 2 {
		for _, lookup := range [][2]string{{"/tmp/repo", "feat/a"}, {"/tmp/repo", "feat/b"}, {"/tmp/other", "feat/a"}} {
			_, err := c.cache.ListBranchPullRequests(t.Context(), lookup[0], lookup[1])
			require.NoError(t, err)
		}
	}

	require.Equal(
		t,
		map[string]int{"/tmp/repo:feat/a": 1, "/tmp/repo:feat/b": 1, "/tmp/other:feat/a": 1},
		c.gh.listings,
	)
}

func TestPullRequestCache_ListsABranchAgainOnceAMinuteHasPassed(t *testing.T) {
	c := newCacheUnderTest(t)
	c.gh.byBranch["feat/billing"] = openPullRequest()
	_, err := c.list(t, "feat/billing")
	require.NoError(t, err)

	c.gh.byBranch["feat/billing"] = []core.PullRequest{{Number: 127, State: core.PRStateMerged}}
	c.after(time.Minute)
	pullRequests, err := c.list(t, "feat/billing")

	require.NoError(t, err)
	require.Equal(t, []core.PullRequest{{Number: 127, State: core.PRStateMerged}}, pullRequests)
	require.Equal(t, 2, c.gh.listings["/tmp/repo:feat/billing"])
}

// A branch whose pull requests can no longer change is listed less often. One
// without any, or with one still open, may get a new one or change any time.
func TestPullRequestCache_CachesASettledBranchForAQuarterOfAnHour(t *testing.T) {
	cases := map[string]struct {
		pullRequests []core.PullRequest
		ttl          time.Duration
	}{
		"merged and closed": {
			[]core.PullRequest{{Number: 120, State: core.PRStateMerged}, {Number: 121, State: core.PRStateClosed}},
			15 * time.Minute,
		},
		"none": {nil, time.Minute},
		"one open": {
			[]core.PullRequest{{Number: 120, State: core.PRStateMerged}, {Number: 127, State: core.PRStateOpen}},
			time.Minute,
		},
		"one draft": {[]core.PullRequest{{Number: 127, State: core.PRStateDraft}}, time.Minute},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := newCacheUnderTest(t)
			c.gh.byBranch["feat/billing"] = tc.pullRequests

			_, err := c.list(t, "feat/billing")
			require.NoError(t, err)
			c.after(tc.ttl - time.Second)
			_, err = c.list(t, "feat/billing")
			require.NoError(t, err)
			require.Equal(t, 1, c.gh.listings["/tmp/repo:feat/billing"])

			c.after(time.Second)
			_, err = c.list(t, "feat/billing")
			require.NoError(t, err)
			require.Equal(t, 2, c.gh.listings["/tmp/repo:feat/billing"])
		})
	}
}

// Offline or signed out of gh, a branch keeps the pull requests last listed,
// and gh is asked again only once the cache would have asked anyway.
func TestPullRequestCache_ServesTheLastListedPullRequestsWhenListingFails(t *testing.T) {
	c := newCacheUnderTest(t)
	c.gh.byBranch["feat/billing"] = openPullRequest()
	_, err := c.list(t, "feat/billing")
	require.NoError(t, err)

	c.gh.err = errors.New("gh: could not resolve host api.github.com")
	c.after(2 * time.Minute)
	for range 2 {
		pullRequests, err := c.list(t, "feat/billing")
		require.NoError(t, err)
		require.Equal(t, openPullRequest(), pullRequests)
		c.after(30 * time.Second)
	}
	require.Equal(t, 2, c.gh.listings["/tmp/repo:feat/billing"])

	// A minute after the failure, gh is asked again.
	c.gh.err = nil
	c.gh.byBranch["feat/billing"] = []core.PullRequest{{Number: 127, State: core.PRStateMerged}}
	pullRequests, err := c.list(t, "feat/billing")
	require.NoError(t, err)
	require.Equal(t, []core.PullRequest{{Number: 127, State: core.PRStateMerged}}, pullRequests)
	require.Equal(t, 3, c.gh.listings["/tmp/repo:feat/billing"])
}

// Without an earlier answer, the failure is what the cache serves, without
// running gh, until a minute has passed.
func TestPullRequestCache_RemembersAFailureForAMinute(t *testing.T) {
	c := newCacheUnderTest(t)
	c.gh.err = errors.New("gh: not logged in")

	_, err := c.list(t, "feat/billing")
	require.ErrorContains(t, err, "gh: not logged in")
	c.after(59 * time.Second)
	_, err = c.list(t, "feat/billing")
	require.ErrorContains(t, err, "gh: not logged in")
	require.Equal(t, 1, c.gh.listings["/tmp/repo:feat/billing"])

	c.gh.err = nil
	c.gh.byBranch["feat/billing"] = openPullRequest()
	c.after(time.Second)
	pullRequests, err := c.list(t, "feat/billing")
	require.NoError(t, err)
	require.Equal(t, openPullRequest(), pullRequests)
	require.Equal(t, 2, c.gh.listings["/tmp/repo:feat/billing"])
}

func TestPullRequestCache_KeepsItsOwnCopy(t *testing.T) {
	c := newCacheUnderTest(t)
	c.gh.byBranch["feat/billing"] = openPullRequest()

	first, err := c.list(t, "feat/billing")
	require.NoError(t, err)
	first[0].State = core.PRStateClosed
	second, err := c.list(t, "feat/billing")
	require.NoError(t, err)

	require.Equal(t, openPullRequest(), second)
	require.Equal(t, openPullRequest(), c.gh.byBranch["feat/billing"])
}

func TestPullRequestCache_CachesEachRepositorysDefaultBranchForAnHour(t *testing.T) {
	c := newCacheUnderTest(t)
	c.gh.defaultBranch = map[string]string{"/tmp/repo": "main", "/tmp/other": "develop"}

	for range 2 {
		branch, err := c.cache.DefaultBranch(t.Context(), "/tmp/repo")
		require.NoError(t, err)
		require.Equal(t, "main", branch)
		branch, err = c.cache.DefaultBranch(t.Context(), "/tmp/other")
		require.NoError(t, err)
		require.Equal(t, "develop", branch)
		c.after(59 * time.Minute)
	}
	require.Equal(t, map[string]int{"/tmp/repo": 1, "/tmp/other": 1}, c.gh.defaultLookups)

	c.gh.defaultBranch["/tmp/repo"] = "trunk"
	c.after(time.Minute)
	branch, err := c.cache.DefaultBranch(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, "trunk", branch)
	require.Equal(t, 2, c.gh.defaultLookups["/tmp/repo"])
}

func TestPullRequestCache_RemembersDefaultBranchFailures(t *testing.T) {
	c := newCacheUnderTest(t)
	c.gh.err = errors.New("gh: not logged in")

	// Without an earlier answer, the failure is served for a minute.
	_, err := c.cache.DefaultBranch(t.Context(), "/tmp/repo")
	require.ErrorContains(t, err, "gh: not logged in")
	c.after(59 * time.Second)
	_, err = c.cache.DefaultBranch(t.Context(), "/tmp/repo")
	require.ErrorContains(t, err, "gh: not logged in")
	require.Equal(t, 1, c.gh.defaultLookups["/tmp/repo"])

	c.gh.err = nil
	c.gh.defaultBranch["/tmp/repo"] = "main"
	c.after(time.Second)
	branch, err := c.cache.DefaultBranch(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, "main", branch)

	// With one, the answer is served for another hour.
	c.gh.err = errors.New("gh: could not resolve host api.github.com")
	c.after(time.Hour)
	branch, err = c.cache.DefaultBranch(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, "main", branch)
	c.after(59 * time.Minute)
	_, err = c.cache.DefaultBranch(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, 3, c.gh.defaultLookups["/tmp/repo"])
}

// The new task pull request picker always lists the repository's open pull
// requests afresh.
func TestPullRequestCache_PassesTheRepositorysOtherCallsThrough(t *testing.T) {
	c := newCacheUnderTest(t)
	c.client.EXPECT().ListRepoPullRequests(mock.Anything, "/tmp/repo").
		Return([]core.RepoPullRequest{{Number: 7}}, nil).
		Twice()
	c.client.EXPECT().HealthCheck(mock.Anything).Return(nil).Once()

	for range 2 {
		prs, err := c.cache.ListRepoPullRequests(t.Context(), "/tmp/repo")
		require.NoError(t, err)
		require.Equal(t, []core.RepoPullRequest{{Number: 7}}, prs)
	}
	require.NoError(t, c.cache.HealthCheck(t.Context()))
}
