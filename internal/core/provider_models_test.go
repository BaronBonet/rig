package core

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func codexModelsFixture() []ProviderModel {
	return []ProviderModel{
		{ID: "gpt-5.5", DisplayName: "GPT-5.5", ReasoningLevels: []string{"low", "medium", "high"}},
		{ID: "gpt-5.5-mini", DisplayName: "GPT-5.5 Mini"},
	}
}

func refreshedCodexModelsFixture() []ProviderModel {
	return []ProviderModel{{ID: "gpt-6", DisplayName: "GPT-6", ReasoningLevels: []string{"high"}}}
}

// expectListModels answers the provider client's ListModels calls with
// listModels, passing each call's number from 1, and returns the count of
// calls made.
func expectListModels(
	client *MockProviderClient,
	listModels func(ctx context.Context, call int) ([]ProviderModel, error),
) *atomic.Int32 {
	var calls atomic.Int32
	client.EXPECT().ListModels(mock.Anything).RunAndReturn(
		func(ctx context.Context) ([]ProviderModel, error) {
			return listModels(ctx, int(calls.Add(1)))
		},
	)
	return &calls
}

// newCodexModelCatalog returns a catalog of Codex alone whose ListModels calls
// listModels, and the count of calls made.
func newCodexModelCatalog(
	t *testing.T,
	listModels func(ctx context.Context, call int) ([]ProviderModel, error),
) (*providerModelCatalog, *atomic.Int32) {
	t.Helper()
	client := NewMockProviderClient(t)
	calls := expectListModels(client, listModels)
	return newProviderModelCatalog(map[Provider]ProviderClient{ProviderCodex: client}), calls
}

type providerModelsResult struct {
	models []ProviderModel
	err    error
}

// listCodexModelsInto lists the catalog's Codex models and sends the result.
func listCodexModelsInto(ctx context.Context, catalog *providerModelCatalog, results chan<- providerModelsResult) {
	models, err := catalog.ListProviderModels(ctx, ProviderCodex)
	results <- providerModelsResult{models: models, err: err}
}

func TestTaskServiceListProviderModels_ServesTheCachedListWithinTheTTL(t *testing.T) {
	svc := newTestTaskService(t)
	clock := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	svc.models.now = func() time.Time { return clock }
	calls := expectListModels(svc.providerClientMock, func(context.Context, int) ([]ProviderModel, error) {
		return codexModelsFixture(), nil
	})

	models, err := svc.service.ListProviderModels(t.Context(), ProviderCodex)
	require.NoError(t, err)
	require.Equal(t, codexModelsFixture(), models)
	require.Equal(t, int32(1), calls.Load())

	clock = clock.Add(providerModelsCacheTTL - time.Second)
	models, err = svc.service.ListProviderModels(t.Context(), ProviderCodex)

	require.NoError(t, err)
	require.Equal(t, codexModelsFixture(), models)
	require.Equal(t, int32(1), calls.Load())
}

func TestTaskServiceListProviderModels_DoesNotCacheErrors(t *testing.T) {
	svc := newTestTaskService(t)
	wantErr := errors.New("list codex models: app-server exited")
	calls := expectListModels(svc.providerClientMock, func(_ context.Context, call int) ([]ProviderModel, error) {
		if call == 1 {
			return nil, wantErr
		}
		return codexModelsFixture(), nil
	})

	_, err := svc.service.ListProviderModels(t.Context(), ProviderCodex)
	require.ErrorIs(t, err, wantErr)

	models, err := svc.service.ListProviderModels(t.Context(), ProviderCodex)

	require.NoError(t, err)
	require.Equal(t, codexModelsFixture(), models)
	require.Equal(t, int32(2), calls.Load())
}

func TestTaskServiceListProviderModels_DoesNotCacheAnEmptyList(t *testing.T) {
	svc := newTestTaskService(t)
	calls := expectListModels(svc.providerClientMock, func(_ context.Context, call int) ([]ProviderModel, error) {
		if call == 1 {
			return []ProviderModel{}, nil
		}
		return codexModelsFixture(), nil
	})

	models, err := svc.service.ListProviderModels(t.Context(), ProviderCodex)
	require.NoError(t, err)
	require.Empty(t, models)

	models, err = svc.service.ListProviderModels(t.Context(), ProviderCodex)

	require.NoError(t, err)
	require.Equal(t, codexModelsFixture(), models)
	require.Equal(t, int32(2), calls.Load())
}

func TestTaskServiceListProviderModels_CachesEachProviderOnItsOwn(t *testing.T) {
	// Claude is supported but not configured in the harness's provider setup:
	// listing its models does not need it to be.
	svc := newTestTaskService(t)
	claudeModels := []ProviderModel{{ID: "opus", DisplayName: "Opus", ReasoningLevels: []string{"low", "high"}}}
	codexCalls := expectListModels(svc.providerClientMock, func(context.Context, int) ([]ProviderModel, error) {
		return codexModelsFixture(), nil
	})
	claudeCalls := expectListModels(svc.claudeClientMock, func(context.Context, int) ([]ProviderModel, error) {
		return claudeModels, nil
	})

	codex, err := svc.service.ListProviderModels(t.Context(), ProviderCodex)
	require.NoError(t, err)
	claude, err := svc.service.ListProviderModels(t.Context(), ProviderClaude)
	require.NoError(t, err)

	require.Equal(t, codexModelsFixture(), codex)
	require.Equal(t, claudeModels, claude)
	require.Equal(t, int32(1), codexCalls.Load())
	require.Equal(t, int32(1), claudeCalls.Load())
}

func TestTaskServiceListProviderModels_RejectsUnsupportedProviders(t *testing.T) {
	svc := newTestTaskService(t)

	_, err := svc.service.ListProviderModels(t.Context(), Provider("gemini"))

	require.ErrorContains(t, err, `provider "gemini" unavailable`)
}

func TestTaskServiceListProviderModels_ReturnsCopiesOfTheCachedList(t *testing.T) {
	svc := newTestTaskService(t)
	expectListModels(svc.providerClientMock, func(context.Context, int) ([]ProviderModel, error) {
		return codexModelsFixture(), nil
	})

	// The first list comes from the fetch and the second from the cache;
	// changing either leaves the cache alone.
	for range 2 {
		models, err := svc.service.ListProviderModels(t.Context(), ProviderCodex)
		require.NoError(t, err)
		models[0].ID = "changed"
		models[0].ReasoningLevels[0] = "changed"
	}
	models, err := svc.service.ListProviderModels(t.Context(), ProviderCodex)

	require.NoError(t, err)
	require.Equal(t, codexModelsFixture(), models)
}

func TestProviderModelCatalog_ConcurrentCallersShareOneFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		catalog, calls := newCodexModelCatalog(t, func(context.Context, int) ([]ProviderModel, error) {
			<-release
			return codexModelsFixture(), nil
		})

		const callers = 5
		results := make(chan providerModelsResult, callers)
		for range callers {
			go listCodexModelsInto(t.Context(), catalog, results)
		}
		// Every caller now waits on one fetch, which waits for release.
		synctest.Wait()
		require.Equal(t, int32(1), calls.Load())
		require.Empty(t, results)

		close(release)
		for range callers {
			result := <-results
			require.NoError(t, result.err)
			require.Equal(t, codexModelsFixture(), result.models)
		}
		require.Equal(t, int32(1), calls.Load())
	})
}

func TestProviderModelCatalog_WaitersShareTheFetchError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		wantErr := errors.New("list codex models: app-server exited")
		catalog, calls := newCodexModelCatalog(t, func(context.Context, int) ([]ProviderModel, error) {
			<-release
			return nil, wantErr
		})

		results := make(chan providerModelsResult, 2)
		go listCodexModelsInto(t.Context(), catalog, results)
		synctest.Wait()
		// The second caller joins the fetch the first one started.
		go listCodexModelsInto(t.Context(), catalog, results)
		synctest.Wait()
		close(release)

		for range 2 {
			require.ErrorIs(t, (<-results).err, wantErr)
		}
		require.Equal(t, int32(1), calls.Load())
	})
}

func TestProviderModelCatalog_CancelledCallerLeavesTheFetchToFillTheCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		catalog, calls := newCodexModelCatalog(t, func(ctx context.Context, _ int) ([]ProviderModel, error) {
			select {
			case <-release:
				return codexModelsFixture(), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})

		ctx, cancel := context.WithCancel(t.Context())
		results := make(chan providerModelsResult, 1)
		go listCodexModelsInto(ctx, catalog, results)
		synctest.Wait()
		cancel()
		require.ErrorIs(t, (<-results).err, context.Canceled)

		// The fetch outlives the caller that started it and fills the cache.
		close(release)
		synctest.Wait()
		models, err := catalog.ListProviderModels(t.Context(), ProviderCodex)

		require.NoError(t, err)
		require.Equal(t, codexModelsFixture(), models)
		require.Equal(t, int32(1), calls.Load())
	})
}

func TestProviderModelCatalog_ServesAStaleListWhileOneRefreshRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		catalog, calls := newCodexModelCatalog(t, func(_ context.Context, call int) ([]ProviderModel, error) {
			if call == 1 {
				return codexModelsFixture(), nil
			}
			<-release
			return refreshedCodexModelsFixture(), nil
		})
		clock := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
		catalog.now = func() time.Time { return clock }
		_, err := catalog.ListProviderModels(t.Context(), ProviderCodex)
		require.NoError(t, err)

		clock = clock.Add(providerModelsCacheTTL)
		const callers = 5
		results := make(chan providerModelsResult, callers)
		for range callers {
			go listCodexModelsInto(t.Context(), catalog, results)
		}

		// Every caller gets the stale list while the one refresh waits for
		// release.
		for range callers {
			result := <-results
			require.NoError(t, result.err)
			require.Equal(t, codexModelsFixture(), result.models)
		}
		synctest.Wait()
		require.Equal(t, int32(2), calls.Load())
		close(release)
	})
}

func TestProviderModelCatalog_ReplacesAStaleListOnceItsRefreshSucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, calls := newCodexModelCatalog(t, func(_ context.Context, call int) ([]ProviderModel, error) {
			if call == 1 {
				return codexModelsFixture(), nil
			}
			return refreshedCodexModelsFixture(), nil
		})
		clock := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
		catalog.now = func() time.Time { return clock }
		_, err := catalog.ListProviderModels(t.Context(), ProviderCodex)
		require.NoError(t, err)

		clock = clock.Add(providerModelsCacheTTL)
		stale, err := catalog.ListProviderModels(t.Context(), ProviderCodex)
		require.NoError(t, err)
		require.Equal(t, codexModelsFixture(), stale)
		synctest.Wait()
		models, err := catalog.ListProviderModels(t.Context(), ProviderCodex)

		require.NoError(t, err)
		require.Equal(t, refreshedCodexModelsFixture(), models)
		require.Equal(t, int32(2), calls.Load())
	})
}

func TestProviderModelCatalog_KeepsAStaleListWhenItsRefreshFailsOrIsEmpty(t *testing.T) {
	tests := map[string]struct {
		refreshModels []ProviderModel
		refreshErr    error
	}{
		"refresh fails":           {refreshErr: errors.New("list codex models: app-server exited")},
		"refresh lists no models": {refreshModels: []ProviderModel{}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				catalog, calls := newCodexModelCatalog(t, func(_ context.Context, call int) ([]ProviderModel, error) {
					if call == 1 {
						return codexModelsFixture(), nil
					}
					return tt.refreshModels, tt.refreshErr
				})
				clock := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
				catalog.now = func() time.Time { return clock }
				_, err := catalog.ListProviderModels(t.Context(), ProviderCodex)
				require.NoError(t, err)

				clock = clock.Add(providerModelsCacheTTL)
				_, err = catalog.ListProviderModels(t.Context(), ProviderCodex)
				require.NoError(t, err)
				synctest.Wait()
				models, err := catalog.ListProviderModels(t.Context(), ProviderCodex)

				require.NoError(t, err)
				require.Equal(t, codexModelsFixture(), models)
				// The list is still stale, so that call started another refresh.
				synctest.Wait()
				require.Equal(t, int32(3), calls.Load())
			})
		})
	}
}

func TestProviderModelCatalog_EndsAFetchAtItsTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		catalog, _ := newCodexModelCatalog(t, func(ctx context.Context, _ int) ([]ProviderModel, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		started := time.Now()

		_, err := catalog.ListProviderModels(t.Context(), ProviderCodex)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, providerModelsFetchTimeout, time.Since(started))
	})
}

func TestProviderModelCatalog_TurnsAPanicIntoAnErrorForEveryWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		catalog, _ := newCodexModelCatalog(t, func(context.Context, int) ([]ProviderModel, error) {
			<-release
			panic("model list out of range")
		})

		const callers = 3
		results := make(chan providerModelsResult, callers)
		for range callers {
			go listCodexModelsInto(t.Context(), catalog, results)
		}
		synctest.Wait()
		close(release)

		for range callers {
			require.EqualError(t, (<-results).err, "list codex models: panic: model list out of range")
		}
	})
}
