package core

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

const (
	// providerModelsCacheTTL is how long a provider's cached model list stays
	// fresh. A caller that finds it stale still gets it at once, and starts a
	// refresh in the background.
	providerModelsCacheTTL = time.Hour
	// providerModelsFetchTimeout bounds one fetch of a provider's models,
	// whatever its callers do, so a provider CLI that never answers cannot
	// hold up the provider's waiting callers for good.
	providerModelsFetchTimeout = 45 * time.Second
)

// providerModelCatalog lists the models each provider offers. Asking a
// provider CLI takes up to about a second, so the catalog caches each
// provider's list in memory and returns it at once, even once it is stale. A
// stale list is refreshed in the background, and a refresh that fails or
// lists no models keeps it. Only a caller with no cached list waits for the
// CLI. At most one fetch per provider runs at a time, and every caller that
// waits shares it.
type providerModelCatalog struct {
	providers map[Provider]ProviderClient
	now       func() time.Time

	mu      sync.Mutex
	cached  map[Provider]cachedProviderModels
	fetches map[Provider]*providerModelFetch
}

// cachedProviderModels is one provider's model list and when it was fetched.
type cachedProviderModels struct {
	fetchedAt time.Time
	models    []ProviderModel
}

// providerModelFetch is one in-flight fetch of a provider's model list. Its
// result is set before done closes.
type providerModelFetch struct {
	done   chan struct{}
	err    error
	models []ProviderModel
}

func newProviderModelCatalog(providers map[Provider]ProviderClient) *providerModelCatalog {
	return &providerModelCatalog{
		providers: providers,
		now:       time.Now,
		cached:    make(map[Provider]cachedProviderModels),
		fetches:   make(map[Provider]*providerModelFetch),
	}
}

func (s *service) ListProviderModels(ctx context.Context, provider Provider) ([]ProviderModel, error) {
	return s.models.ListProviderModels(ctx, provider)
}

// ListProviderModels returns the provider's cached model list at once,
// starting a background refresh when it is stale. With no cached list, it
// waits for a fetch from the provider CLI or for ctx, whichever ends first.
// Callers get copies they may change freely.
func (c *providerModelCatalog) ListProviderModels(ctx context.Context, provider Provider) ([]ProviderModel, error) {
	providerClient, err := supportedProviderClient(c.providers, provider)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	cached, isCached := c.cached[provider]
	fetch, isFetching := c.fetches[provider]
	if !isFetching && (!isCached || c.now().Sub(cached.fetchedAt) >= providerModelsCacheTTL) {
		fetch = &providerModelFetch{done: make(chan struct{})}
		c.fetches[provider] = fetch
		// The fetch is shared, so no caller's ctx may cancel it: a TUI that
		// quits mid-fetch must not fail the other callers, and the result
		// still fills the cache.
		go c.fetch(context.WithoutCancel(ctx), provider, providerClient, fetch)
	}
	c.mu.Unlock()

	if isCached {
		return cloneProviderModels(cached.models), nil
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-fetch.done:
	}
	if fetch.err != nil {
		return nil, fetch.err
	}

	return cloneProviderModels(fetch.models), nil
}

// fetch asks the provider CLI for its models within
// providerModelsFetchTimeout, caches a list that has any, and then releases
// the fetch's waiters. A panic in the provider client becomes the fetch's
// error: listing models is not worth stopping the daemon for, and the waiters
// must still be released.
func (c *providerModelCatalog) fetch(
	ctx context.Context,
	provider Provider,
	providerClient ProviderClient,
	fetch *providerModelFetch,
) {
	var (
		models []ProviderModel
		err    error
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			models, err = nil, fmt.Errorf("list %s models: panic: %v", provider, recovered)
		}

		c.mu.Lock()
		defer c.mu.Unlock()
		// An empty list is not cached: a CLI that is logged out, say, may list
		// no models, and the user should not wait an hour after fixing it.
		if err == nil && len(models) > 0 {
			c.cached[provider] = cachedProviderModels{fetchedAt: c.now(), models: models}
		}
		delete(c.fetches, provider)
		fetch.models, fetch.err = models, err
		close(fetch.done)
	}()

	ctx, cancel := context.WithTimeout(ctx, providerModelsFetchTimeout)
	defer cancel()
	models, err = providerClient.ListModels(ctx)
}

func cloneProviderModels(models []ProviderModel) []ProviderModel {
	cloned := slices.Clone(models)
	for i := range cloned {
		cloned[i].ReasoningLevels = slices.Clone(cloned[i].ReasoningLevels)
	}
	return cloned
}
