package claude

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

const (
	// contextWindowProbeTimeout bounds one context window probe, from
	// starting Claude to its get_context_usage response.
	contextWindowProbeTimeout = 30 * time.Second
	// probeInitializeRequestID and contextWindowRequestID identify a probe's
	// initialize and get_context_usage control requests.
	probeInitializeRequestID = "rig-initialize"
	contextWindowRequestID   = "rig-context-window"
	// contextWindowTTL is how long a probed context window stays fresh. A
	// stale one is still served while a background probe refreshes it.
	contextWindowTTL = time.Hour
	// failedContextWindowTTL is how long a model whose probe failed goes
	// without another.
	failedContextWindowTTL = 5 * time.Minute
	// unknownModelSource is the autocompactSource Claude reports for a model
	// it does not know, whose window it then guesses.
	unknownModelSource = "unknown-model"
)

// probeContextWindow asks Claude for model's context window with the
// get_context_usage control request of its stream-json protocol, the request
// behind the Claude Agent SDK's getContextUsage. Claude answers it without a
// model request, so the probe spends no tokens. The window is Claude's
// rawMaxTokens: the model's context window, or the smaller auto-compact
// window the user configured. Claude runs isolated, as startControlSession
// starts it, on model.
//
// A model Claude does not know is an error, though Claude answers with a
// guess for its window: the daemon's Claude may be older than the agent's, or
// lack the setup, such as Bedrock or a gateway, that the agent runs on.
func (r *repository) probeContextWindow(ctx context.Context, model string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, contextWindowProbeTimeout)
	defer cancel()

	session, err := r.startControlSession(ctx, "--model", model)
	if err != nil {
		return 0, fmt.Errorf("probe claude context window of %s: %w", model, err)
	}
	defer func() { _ = session.Close() }()

	window, err := contextWindow(session)
	if err != nil {
		return 0, fmt.Errorf("probe claude context window of %s: %w", model, err)
	}

	return window, nil
}

// contextUsageResponse is the part of a get_context_usage response's payload
// the probe reads.
type contextUsageResponse struct {
	RawMaxTokens int `json:"rawMaxTokens"`
	// AutocompactSource says where Claude took the window from.
	AutocompactSource string `json:"autocompactSource"`
}

// contextWindow initializes Claude, as the Agent SDK does before any other
// control request, and then asks it for a summary of its context usage.
func contextWindow(session subprocess.LineSession) (int, error) {
	if err := exchangeControlRequest(
		session,
		probeInitializeRequestID,
		controlRequestPayload{Subtype: "initialize"},
		nil,
	); err != nil {
		return 0, err
	}

	var usage contextUsageResponse
	if err := exchangeControlRequest(
		session,
		contextWindowRequestID,
		controlRequestPayload{Subtype: "get_context_usage", Detail: "summary"},
		&usage,
	); err != nil {
		return 0, err
	}
	if usage.AutocompactSource == unknownModelSource {
		return 0, fmt.Errorf("claude does not know the model")
	}
	if usage.RawMaxTokens <= 0 {
		return 0, fmt.Errorf("get_context_usage reported no rawMaxTokens")
	}

	return usage.RawMaxTokens, nil
}

// contextWindowCache remembers the context window Claude reports for each
// model, by the model name a conversation records. A probe takes about a
// second, so a lookup never waits for one: a model without a window starts a
// probe in the background and reads as 0 meanwhile. A window is served for
// an hour and, once stale, still served while a background probe refreshes
// it. A probe that fails keeps the last window, and the model goes five
// minutes without another. At most one probe per model runs at a time, and
// the lookups meanwhile share it.
type contextWindowCache struct {
	probe func(ctx context.Context, model string) (int, error)
	now   func() time.Time
	// probed, when set, is called once a probe's result is cached.
	probed func(model string)

	mu      sync.Mutex
	entries map[string]contextWindowEntry
}

// contextWindowEntry is what the cache knows about one model's window.
type contextWindowEntry struct {
	// window is the latest window a probe reported, or 0 before one has.
	window   int
	probedAt time.Time
	// failedAt is when the latest probe failed, or zero when it succeeded.
	failedAt time.Time
	probing  bool
}

func newContextWindowCache(
	probe func(ctx context.Context, model string) (int, error),
	now func() time.Time,
) *contextWindowCache {
	return &contextWindowCache{probe: probe, now: now, entries: make(map[string]contextWindowEntry)}
}

// lookup returns model's cached context window, or 0 when there is none yet,
// model is empty, or there is no cache. When the model is due a probe and none is running, it
// starts one in the background. The probe keeps ctx's values but not its
// cancellation, as its result outlives the lookup.
func (c *contextWindowCache) lookup(ctx context.Context, model string) int {
	model = strings.TrimSpace(model)
	if c == nil || model == "" {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[model]
	if !entry.probing && entry.due(c.now()) {
		entry.probing = true
		c.entries[model] = entry
		go c.refresh(context.WithoutCancel(ctx), model)
	}

	return entry.window
}

// due reports whether the entry wants a probe at now: when it has no window,
// when its window is stale, or when its latest probe failed long enough ago.
func (e contextWindowEntry) due(now time.Time) bool {
	switch {
	case !e.failedAt.IsZero():
		return now.Sub(e.failedAt) >= failedContextWindowTTL
	case e.window > 0:
		return now.Sub(e.probedAt) >= contextWindowTTL
	default:
		return true
	}
}

// refresh probes model's window and caches the result. A probe that reports
// no window counts as a failure, as a cached window of 0 would be probed
// again at every lookup. So does a panic in the probe: a context window is not
// worth stopping the daemon for, and the model must not be left probing for
// good.
func (c *contextWindowCache) refresh(ctx context.Context, model string) {
	var (
		window int
		err    error
	)
	defer func() {
		failed := recover() != nil || err != nil || window <= 0

		c.mu.Lock()
		entry := c.entries[model]
		entry.probing = false
		if failed {
			entry.failedAt = c.now()
		} else {
			entry.window, entry.probedAt, entry.failedAt = window, c.now(), time.Time{}
		}
		c.entries[model] = entry
		c.mu.Unlock()

		if c.probed != nil {
			c.probed(model)
		}
	}()

	window, err = c.probe(ctx, model)
}
