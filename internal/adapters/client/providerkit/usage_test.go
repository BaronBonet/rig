package providerkit

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// fakeClock is a settable time source for a UsageCache.
type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time { return c.at }

func (c *fakeClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newTestUsageCache() (*UsageCache, *fakeClock) {
	clock := &fakeClock{at: time.Date(2026, time.October, 13, 9, 0, 0, 0, time.UTC)}
	cache := NewUsageCache()
	cache.now = clock.now
	return cache, clock
}

func TestUsageCache_ReusesAReadingWithinTheTTL(t *testing.T) {
	cache, clock := newTestUsageCache()
	reads := 0
	read := func() (*core.AccountUsage, error) {
		reads++
		return &core.AccountUsage{Plan: "max"}, nil
	}

	first, err := cache.Get("acct", read)
	require.NoError(t, err)
	clock.advance(usageCacheTTL - time.Second)
	second, err := cache.Get("acct", read)
	require.NoError(t, err)

	require.Equal(t, 1, reads)
	require.Same(t, first, second)

	clock.advance(2 * time.Second)
	_, err = cache.Get("acct", read)
	require.NoError(t, err)
	require.Equal(t, 2, reads, "a reading is read again once its TTL has passed")
}

func TestUsageCache_KeepsEachAccountsReadingApart(t *testing.T) {
	cache, _ := newTestUsageCache()
	read := func(plan string) func() (*core.AccountUsage, error) {
		return func() (*core.AccountUsage, error) { return &core.AccountUsage{Plan: plan}, nil }
	}

	work, err := cache.Get("work", read("max"))
	require.NoError(t, err)
	personal, err := cache.Get("personal", read("pro"))
	require.NoError(t, err)
	again, err := cache.Get("work", read("never read"))
	require.NoError(t, err)

	require.Equal(t, "max", work.Plan)
	require.Equal(t, "pro", personal.Plan)
	require.Same(t, work, again)
}

func TestUsageCache_CachesAnErrorForTheShorterErrorTTL(t *testing.T) {
	cache, clock := newTestUsageCache()
	reads := 0
	failure := errors.New("not logged in")
	read := func() (*core.AccountUsage, error) {
		reads++
		if reads == 1 {
			return nil, failure
		}
		return &core.AccountUsage{Plan: "max"}, nil
	}

	_, err := cache.Get("acct", read)
	require.ErrorIs(t, err, failure)

	clock.advance(usageErrorCacheTTL - time.Second)
	usage, err := cache.Get("acct", read)
	require.ErrorIs(t, err, failure, "the error is served from the cache")
	require.Nil(t, usage)
	require.Equal(t, 1, reads)

	// Past the error TTL but well within the TTL of a good reading.
	clock.advance(2 * time.Second)
	require.Less(t, usageErrorCacheTTL, usageCacheTTL)
	usage, err = cache.Get("acct", read)
	require.NoError(t, err, "a fresh login shows up once the error has expired")
	require.Equal(t, "max", usage.Plan)
	require.Equal(t, 2, reads)

	// The reading that replaced the error lives for the full TTL.
	clock.advance(usageCacheTTL - time.Second)
	_, err = cache.Get("acct", read)
	require.NoError(t, err)
	require.Equal(t, 2, reads)
}

func TestUsageCache_ANilCacheReadsEveryTime(t *testing.T) {
	var cache *UsageCache
	reads := 0
	read := func() (*core.AccountUsage, error) {
		reads++
		return &core.AccountUsage{}, nil
	}

	for range 3 {
		_, err := cache.Get("acct", read)
		require.NoError(t, err)
	}

	require.Equal(t, 3, reads)
}

func TestGetUsageJSON_SendsTheHeadersAndDecodesTheAnswer(t *testing.T) {
	var method string
	var seen http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		seen = r.Header.Clone()
		_, _ = w.Write([]byte(`{"plan":"max","percent":42.5}`))
	}))
	defer server.Close()

	var out struct {
		Plan    string  `json:"plan"`
		Percent float64 `json:"percent"`
	}
	err := GetUsageJSON(t.Context(), server.URL, map[string]string{
		"Authorization":  "Bearer tok",
		"anthropic-beta": "oauth-2025-04-20",
	}, &out)

	require.NoError(t, err)
	require.Equal(t, "max", out.Plan)
	require.InDelta(t, 42.5, out.Percent, 0.0001)
	require.Equal(t, http.MethodGet, method)
	require.Equal(t, "Bearer tok", seen.Get("Authorization"))
	require.Equal(t, "oauth-2025-04-20", seen.Get("anthropic-beta"))
}

func TestGetUsageJSON_ReportsWhyTheEndpointDidNotAnswer(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: "the login was not accepted"},
		{name: "forbidden", status: http.StatusForbidden, wantErr: "the login was not accepted"},
		{name: "too many requests", status: http.StatusTooManyRequests, wantErr: "too often"},
		{name: "server error", status: http.StatusInternalServerError, wantErr: "usage request failed: 500"},
		{name: "not found", status: http.StatusNotFound, wantErr: "usage request failed: 404"},
		{name: "invalid json", status: http.StatusOK, body: `{"plan":`, wantErr: "decode usage"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			var out map[string]any
			err := GetUsageJSON(t.Context(), server.URL, nil, &out)

			require.ErrorContains(t, err, test.wantErr)
			require.Empty(t, out)
		})
	}
}

func TestGetUsageJSON_MentionsTheLoginWhenItIsNotAccepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	err := GetUsageJSON(t.Context(), server.URL, nil, &struct{}{})

	require.ErrorContains(t, err, "login")
	require.ErrorContains(t, err, "401")
}

func TestGetUsageJSON_FailsWhenTheEndpointCannotBeReached(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	require.ErrorContains(t, GetUsageJSON(t.Context(), url, nil, &struct{}{}), "ask for usage")
	require.ErrorContains(t, GetUsageJSON(t.Context(), "://bad", nil, &struct{}{}), "build usage request")
}
