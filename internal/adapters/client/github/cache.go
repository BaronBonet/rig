package github

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/BaronBonet/rig/internal/core"
)

const (
	// branchPullRequestsTTL is how long a branch's pull requests are served
	// from the cache while one may still change: while it has an open or
	// draft pull request, or none yet.
	branchPullRequestsTTL = time.Minute
	// settledBranchPullRequestsTTL is how long a branch's pull requests are
	// served from the cache once it has some and each is merged or closed, so
	// a task's history costs few GitHub API calls.
	settledBranchPullRequestsTTL = 15 * time.Minute
	// defaultBranchTTL is how long a repository's default branch is served
	// from the cache, as it seldom changes.
	defaultBranchTTL = time.Hour
	// failedLookupTTL is how long a lookup that failed, and has no earlier
	// answer to fall back on, keeps failing without asking gh again.
	failedLookupTTL = time.Minute
)

// pullRequestCache is a core.PullRequestClient that remembers each branch's
// pull requests and each repository's default branch, so the task list's
// refreshes seldom run gh. The new task pull request picker's listing goes
// to the client it wraps every time.
type pullRequestCache struct {
	core.PullRequestClient

	branches        *lookupCache[branchPullRequestsKey, []core.PullRequest]
	defaultBranches *lookupCache[string, string]
}

type branchPullRequestsKey struct {
	repoRoot   string
	branchName string
}

// NewPullRequestCache wraps client so that each branch's pull requests, by
// repository root and branch name, are listed again only after a minute, or
// after a quarter of an hour once all of them are merged or closed, and each
// repository's default branch is looked up again only after an hour. A
// lookup that fails serves the last answer for another such while, so a
// passing failure, as offline or with gh signed out, never makes pull
// requests disappear. Without an earlier answer, the failure is served for a
// minute. Either way, gh is not run again until then.
func NewPullRequestCache(client core.PullRequestClient) core.PullRequestClient {
	return newPullRequestCache(client, time.Now)
}

func newPullRequestCache(client core.PullRequestClient, now func() time.Time) *pullRequestCache {
	return &pullRequestCache{
		PullRequestClient: client,
		branches:          newLookupCache[branchPullRequestsKey](now, branchPullRequestsTTLFor),
		defaultBranches: newLookupCache[string](now, func(string) time.Duration {
			return defaultBranchTTL
		}),
	}
}

func (c *pullRequestCache) ListBranchPullRequests(
	ctx context.Context,
	repoRoot string,
	branchName string,
) ([]core.PullRequest, error) {
	key := branchPullRequestsKey{repoRoot: repoRoot, branchName: branchName}
	pullRequests, err := c.branches.get(key, func() ([]core.PullRequest, error) {
		pullRequests, err := c.PullRequestClient.ListBranchPullRequests(ctx, repoRoot, branchName)
		return slices.Clone(pullRequests), err
	})
	return slices.Clone(pullRequests), err
}

func (c *pullRequestCache) DefaultBranch(ctx context.Context, repoRoot string) (string, error) {
	return c.defaultBranches.get(repoRoot, func() (string, error) {
		return c.PullRequestClient.DefaultBranch(ctx, repoRoot)
	})
}

// branchPullRequestsTTLFor is how long a branch's pull requests are served
// from the cache: longer once the branch has pull requests and none of them
// can change any more.
func branchPullRequestsTTLFor(pullRequests []core.PullRequest) time.Duration {
	if len(pullRequests) == 0 {
		return branchPullRequestsTTL
	}
	for _, pullRequest := range pullRequests {
		if pullRequest.State != core.PRStateMerged && pullRequest.State != core.PRStateClosed {
			return branchPullRequestsTTL
		}
	}
	return settledBranchPullRequestsTTL
}

// lookupCache remembers the answer of a lookup by key until it expires,
// including a failure.
type lookupCache[K comparable, V any] struct {
	now     func() time.Time
	ttl     func(V) time.Duration
	entries map[K]cachedLookup[V]
	mu      sync.Mutex
}

// cachedLookup is a lookup's latest answer: its value, or the error of a
// lookup that has never succeeded, until expiresAt.
type cachedLookup[V any] struct {
	expiresAt time.Time
	err       error
	value     V
	// succeeded records that value is a lookup's answer, which a later
	// failure falls back on.
	succeeded bool
}

func newLookupCache[K comparable, V any](now func() time.Time, ttl func(V) time.Duration) *lookupCache[K, V] {
	return &lookupCache[K, V]{now: now, ttl: ttl, entries: map[K]cachedLookup[V]{}}
}

// get returns the answer cached for key until it expires, and then looks it
// up afresh. A lookup that fails keeps the last value that succeeded, for as
// long again as it was kept; with no such value, it keeps the failure for
// failedLookupTTL. The lock is not held during a lookup, so lookups of other
// keys go on meanwhile, and two lookups of one key at once both run.
func (c *lookupCache[K, V]) get(key K, lookup func() (V, error)) (V, error) {
	c.mu.Lock()
	entry, found := c.entries[key]
	c.mu.Unlock()
	if found && c.now().Before(entry.expiresAt) {
		return entry.value, entry.err
	}

	value, err := lookup()
	switch {
	case err == nil:
		entry = cachedLookup[V]{value: value, succeeded: true, expiresAt: c.now().Add(c.ttl(value))}
	case found && entry.succeeded:
		entry.expiresAt = c.now().Add(c.ttl(entry.value))
	default:
		entry = cachedLookup[V]{err: err, expiresAt: c.now().Add(failedLookupTTL)}
	}

	c.mu.Lock()
	c.entries[key] = entry
	c.mu.Unlock()
	return entry.value, entry.err
}
