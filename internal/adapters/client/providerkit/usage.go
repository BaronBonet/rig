package providerkit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/BaronBonet/rig/internal/core"
)

const (
	// usageCacheTTL is how long an account's usage reading is reused. The
	// providers' usage endpoints rate limit their callers, and the TUI asks
	// whenever a composer opens.
	usageCacheTTL = time.Minute
	// usageErrorCacheTTL is how long a failed reading is reused, short so a
	// fresh login shows up soon.
	usageErrorCacheTTL  = 15 * time.Second
	usageRequestTimeout = 10 * time.Second
	// UsageReadTimeout bounds a whole usage reading, reading the stored
	// login included, so a locked keychain cannot hold it up.
	UsageReadTimeout = 20 * time.Second
)

// UsageCache keeps each account's latest usage reading for a while, so a
// provider's usage endpoint is asked at most about once a minute per account.
// A nil UsageCache reads every time.
type UsageCache struct {
	mu      sync.Mutex
	entries map[string]usageCacheEntry
	now     func() time.Time
}

type usageCacheEntry struct {
	usage    *core.AccountUsage
	err      error
	expireAt time.Time
}

func NewUsageCache() *UsageCache {
	return &UsageCache{entries: make(map[string]usageCacheEntry), now: time.Now}
}

// Get returns the cached reading for the account key, or reads it.
func (c *UsageCache) Get(key string, read func() (*core.AccountUsage, error)) (*core.AccountUsage, error) {
	if c == nil {
		return read()
	}
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && c.now().Before(entry.expireAt) {
		return entry.usage, entry.err
	}

	usage, err := read()
	ttl := usageCacheTTL
	if err != nil {
		ttl = usageErrorCacheTTL
	}
	c.mu.Lock()
	c.entries[key] = usageCacheEntry{usage: usage, err: err, expireAt: c.now().Add(ttl)}
	c.mu.Unlock()
	return usage, err
}

// GetUsageJSON asks a provider's usage endpoint and decodes its JSON answer
// into out. A 401 or 403 means the account's login was not accepted.
func GetUsageJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, usageRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build usage request: %w", err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("ask for usage: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the login was not accepted (%s); start a session on this account to renew it",
			response.Status)
	case response.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("usage was asked for too often; try again in a few minutes")
	case response.StatusCode < 200 || response.StatusCode > 299:
		return fmt.Errorf("usage request failed: %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read usage: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode usage: %w", err)
	}
	return nil
}
