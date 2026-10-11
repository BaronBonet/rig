package claude

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// probeWaitTimeout bounds how long a test waits for a background probe, so a
// probe that never starts or finishes fails the test instead of hanging it.
const probeWaitTimeout = 5 * time.Second

// contextWindowHarness drives a context window cache whose probes the test
// answers one at a time, on a clock the test sets.
type contextWindowHarness struct {
	cache *contextWindowCache
	now   time.Time
	// probes counts the probes started.
	probes atomic.Int32
	// started receives the model of each probe as it starts, and results
	// answers it.
	started chan string
	results chan probeResult
	// probed receives the model of each probe once the cache holds its
	// result.
	probed chan string
}

type probeResult struct {
	window int
	err    error
}

func newContextWindowHarness(t *testing.T) *contextWindowHarness {
	t.Helper()
	h := &contextWindowHarness{
		now:     time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC),
		started: make(chan string, 16),
		results: make(chan probeResult),
		probed:  make(chan string, 16),
	}
	h.cache = newContextWindowCache(
		func(_ context.Context, model string) (int, error) {
			h.probes.Add(1)
			h.started <- model
			result := <-h.results
			return result.window, result.err
		},
		func() time.Time { return h.now },
	)
	h.cache.probed = func(model string) { h.probed <- model }
	t.Cleanup(func() {
		h.cache.mu.Lock()
		defer h.cache.mu.Unlock()
		for model, entry := range h.cache.entries {
			require.False(t, entry.probing, "a probe of %s is still running", model)
		}
	})
	return h
}

// probing reports whether a probe of model is running.
func (h *contextWindowHarness) probing(model string) bool {
	h.cache.mu.Lock()
	defer h.cache.mu.Unlock()
	return h.cache.entries[model].probing
}

// finishProbe waits for the probe of model to start, answers it, and waits
// for the cache to hold its result.
func (h *contextWindowHarness) finishProbe(t *testing.T, model string, window int, err error) {
	t.Helper()
	require.Equal(t, model, receiveWithin(t, h.started))
	h.results <- probeResult{window: window, err: err}
	require.Equal(t, model, receiveWithin(t, h.probed))
}

func receiveWithin[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(probeWaitTimeout):
		require.FailNow(t, "timed out waiting for a background probe")
		var zero T
		return zero
	}
}

func TestContextWindowCache_ServesTheWindowOnceTheProbeOfAMissFinishes(t *testing.T) {
	h := newContextWindowHarness(t)

	require.Equal(t, 0, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.True(t, h.probing("claude-opus-5-5"))
	h.finishProbe(t, "claude-opus-5-5", 1000000, nil)

	require.Equal(t, 1000000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.False(t, h.probing("claude-opus-5-5"))
	require.Equal(t, int32(1), h.probes.Load())
}

func TestContextWindowCache_ConcurrentMissesShareOneProbe(t *testing.T) {
	h := newContextWindowHarness(t)

	var lookups sync.WaitGroup
	windows := make([]int, 8)
	for i := range windows {
		lookups.Go(func() { windows[i] = h.cache.lookup(t.Context(), "claude-sonnet-4-6") })
	}
	lookups.Wait()
	require.Equal(t, make([]int, 8), windows)
	h.finishProbe(t, "claude-sonnet-4-6", 200000, nil)

	require.Equal(t, 200000, h.cache.lookup(t.Context(), "claude-sonnet-4-6"))
	require.Equal(t, int32(1), h.probes.Load())
}

// The probe's result outlives the lookup that started it, so the caller's
// cancellation does not reach it.
func TestContextWindowCache_ProbesBeyondTheLookupsContext(t *testing.T) {
	h := newContextWindowHarness(t)
	probeErr := make(chan error, 1)
	probe := h.cache.probe
	h.cache.probe = func(ctx context.Context, model string) (int, error) {
		probeErr <- ctx.Err()
		return probe(ctx, model)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.Equal(t, 0, h.cache.lookup(ctx, "claude-opus-5-5"))
	h.finishProbe(t, "claude-opus-5-5", 1000000, nil)

	require.NoError(t, receiveWithin(t, probeErr))
	require.Equal(t, 1000000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
}

func TestContextWindowCache_RetriesAFailedProbeOnlyAfterFiveMinutes(t *testing.T) {
	h := newContextWindowHarness(t)

	require.Equal(t, 0, h.cache.lookup(t.Context(), "claude-opus-5[1m]"))
	h.finishProbe(t, "claude-opus-5[1m]", 0, errors.New("model unavailable"))

	h.now = h.now.Add(failedContextWindowTTL - time.Second)
	require.Equal(t, 0, h.cache.lookup(t.Context(), "claude-opus-5[1m]"))
	require.False(t, h.probing("claude-opus-5[1m]"))

	h.now = h.now.Add(time.Second)
	require.Equal(t, 0, h.cache.lookup(t.Context(), "claude-opus-5[1m]"))
	require.True(t, h.probing("claude-opus-5[1m]"))
	h.finishProbe(t, "claude-opus-5[1m]", 1000000, nil)

	require.Equal(t, 1000000, h.cache.lookup(t.Context(), "claude-opus-5[1m]"))
	require.Equal(t, int32(2), h.probes.Load())
}

func TestContextWindowCache_ServesAStaleWindowWhileRefreshingIt(t *testing.T) {
	h := newContextWindowHarness(t)
	h.cache.lookup(t.Context(), "claude-opus-5-5")
	h.finishProbe(t, "claude-opus-5-5", 200000, nil)

	h.now = h.now.Add(contextWindowTTL - time.Second)
	require.Equal(t, 200000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.False(t, h.probing("claude-opus-5-5"))

	h.now = h.now.Add(time.Second)
	require.Equal(t, 200000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.True(t, h.probing("claude-opus-5-5"))
	require.Equal(t, 200000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	h.finishProbe(t, "claude-opus-5-5", 1000000, nil)

	require.Equal(t, 1000000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.False(t, h.probing("claude-opus-5-5"))
	require.Equal(t, int32(2), h.probes.Load())
}

// A refresh that fails keeps the last window, and the model goes five
// minutes without another probe.
func TestContextWindowCache_KeepsTheLastWindowWhenARefreshFails(t *testing.T) {
	h := newContextWindowHarness(t)
	h.cache.lookup(t.Context(), "claude-opus-5-5")
	h.finishProbe(t, "claude-opus-5-5", 200000, nil)
	h.now = h.now.Add(contextWindowTTL)
	h.cache.lookup(t.Context(), "claude-opus-5-5")
	h.finishProbe(t, "claude-opus-5-5", 0, errors.New("claude ended"))

	h.now = h.now.Add(failedContextWindowTTL - time.Second)
	require.Equal(t, 200000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.False(t, h.probing("claude-opus-5-5"))

	h.now = h.now.Add(time.Second)
	require.Equal(t, 200000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	h.finishProbe(t, "claude-opus-5-5", 1000000, nil)

	require.Equal(t, 1000000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.Equal(t, int32(3), h.probes.Load())
}

// A probe that reports no window is a failure, or every lookup would probe
// again.
func TestContextWindowCache_CountsAProbeWithoutAWindowAsAFailure(t *testing.T) {
	h := newContextWindowHarness(t)
	h.cache.lookup(t.Context(), "claude-opus-5-5")
	h.finishProbe(t, "claude-opus-5-5", 0, nil)

	h.now = h.now.Add(failedContextWindowTTL - time.Second)
	require.Equal(t, 0, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.False(t, h.probing("claude-opus-5-5"))

	h.now = h.now.Add(time.Second)
	h.cache.lookup(t.Context(), "claude-opus-5-5")
	h.finishProbe(t, "claude-opus-5-5", 1000000, nil)

	require.Equal(t, 1000000, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.Equal(t, int32(2), h.probes.Load())
}

func TestContextWindowCache_CountsAPanickingProbeAsAFailure(t *testing.T) {
	h := newContextWindowHarness(t)
	h.cache.probe = func(context.Context, string) (int, error) { panic("probe broke") }

	require.Equal(t, 0, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.Equal(t, "claude-opus-5-5", receiveWithin(t, h.probed))

	require.Equal(t, 0, h.cache.lookup(t.Context(), "claude-opus-5-5"))
	require.False(t, h.probing("claude-opus-5-5"))
	h.cache.mu.Lock()
	defer h.cache.mu.Unlock()
	require.Equal(t, h.now, h.cache.entries["claude-opus-5-5"].failedAt)
}

// A repository built without New has no cache, and reads no window.
func TestReadConversationUsage_ReadsNoWindowWithoutACache(t *testing.T) {
	transcript := writeTranscript(t, claudeAssistantEntry(false, "claude-opus-5-5", 3, 12000, 8000))

	usage, err := (&repository{}).ReadConversationUsage(t.Context(), []core.TaskProviderSession{{
		Provider:       core.ProviderClaude,
		TranscriptPath: transcript,
		Model:          "claude-opus-5-5",
	}})

	require.NoError(t, err)
	require.Equal(t, core.ConversationUsage{Context: &core.ContextUsage{Tokens: 20003}}, usage)
}

func TestContextWindowCache_NeverProbesAnEmptyModel(t *testing.T) {
	h := newContextWindowHarness(t)

	require.Equal(t, 0, h.cache.lookup(t.Context(), ""))
	require.Equal(t, 0, h.cache.lookup(t.Context(), "  "))

	require.Empty(t, h.cache.entries)
	require.Equal(t, int32(0), h.probes.Load())
}

// knownModelUsage is how Claude reports the window of a model it knows.
const knownModelUsage = `"rawMaxTokens":1000000,"autocompactSource":"model-default",`

// contextUsageReply is a get_context_usage summary response to Rig's probe,
// shaped as Claude Code 2.1.296 answers it, with fields spliced in.
func contextUsageReply(fields string) string {
	return `{"type":"control_response","response":{"subtype":"success","request_id":"` + contextWindowRequestID +
		`","response":{"model":"claude-opus-5-5","totalTokens":0,"maxTokens":1000000,` + fields +
		`"autoCompactThreshold":967000,"percentage":0}}}`
}

// initializeReply is a successful initialize response to Rig's probe.
const initializeReply = `{"type":"control_response","response":{"subtype":"success","request_id":"` +
	probeInitializeRequestID + `","response":{"models":[{"value":"opus","displayName":"Opus"}]}}}`

// newContextWindowProbeRepository returns a repository whose one Claude
// session replays replies. The runner expects Claude's stream-json probe on
// claude-opus-5-5, under a deadline, in Rig's private control directory,
// which exists while the session starts.
func newContextWindowProbeRepository(t *testing.T, replies ...string) (*repository, *fakeLineSession) {
	t.Helper()
	session := &fakeLineSession{replies: replies}
	var controlDir string
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().StartLineSession(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, opts subprocess.StartLineSessionOptions) (subprocess.LineSession, error) {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			require.Equal(t, "claude", opts.Name)
			require.Equal(t, []string{
				"-p",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--verbose",
				"--no-session-persistence",
				"--setting-sources", "user",
				"--settings", `{"disableAllHooks":true}`,
				"--model", "claude-opus-5-5",
			}, opts.Args)
			require.Equal(t, controlDir, opts.Cwd)
			info, err := os.Stat(opts.Cwd)
			require.NoError(t, err)
			require.True(t, info.IsDir())
			require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
			session.cwd = opts.Cwd
			return session, nil
		},
	).Once()

	repo, dataDir := newTestRepository(t, runner)
	controlDir = filepath.Join(dataDir, "claude", "control")
	return repo, session
}

func TestRepositoryProbeContextWindow_ReadsRawMaxTokens(t *testing.T) {
	repo, session := newContextWindowProbeRepository(
		t,
		`{"type":"system","subtype":"hook_started"}`,
		initializeReply,
		`{"type":"control_response","response":{"subtype":"error","request_id":"other","error":"not ours"}}`,
		contextUsageReply(knownModelUsage),
	)

	window, err := repo.probeContextWindow(t.Context(), "claude-opus-5-5")

	require.NoError(t, err)
	require.Equal(t, 1000000, window)
	require.Equal(t, []string{
		`{"type":"control_request","request_id":"` + probeInitializeRequestID + `","request":{"subtype":"initialize"}}`,
		`{"type":"control_request","request_id":"` + contextWindowRequestID +
			`","request":{"subtype":"get_context_usage","detail":"summary"}}`,
	}, session.sent)
	require.True(t, session.closed)
	require.DirExists(t, session.cwd)
}

func TestRepositoryProbeContextWindow_ReportsAnErrorResponse(t *testing.T) {
	repo, session := newContextWindowProbeRepository(
		t,
		initializeReply,
		`{"type":"control_response","response":{"subtype":"error","request_id":"`+contextWindowRequestID+`",`+
			`"error":"model not available"}}`,
	)

	_, err := repo.probeContextWindow(t.Context(), "claude-opus-5-5")

	require.ErrorContains(t, err, "get_context_usage failed: model not available")
	require.True(t, session.closed)
}

func TestRepositoryProbeContextWindow_ReportsAFailedInitialize(t *testing.T) {
	repo, session := newContextWindowProbeRepository(
		t,
		`{"type":"control_response","response":{"subtype":"error","request_id":"`+probeInitializeRequestID+`",`+
			`"error":"not logged in"}}`,
	)

	_, err := repo.probeContextWindow(t.Context(), "claude-opus-5-5")

	require.ErrorContains(t, err, "initialize failed: not logged in")
	require.Len(t, session.sent, 1)
}

func TestRepositoryProbeContextWindow_ReportsAResponseWithoutRawMaxTokens(t *testing.T) {
	repo, _ := newContextWindowProbeRepository(
		t,
		initializeReply,
		contextUsageReply(`"autocompactSource":"model-default",`),
	)

	_, err := repo.probeContextWindow(t.Context(), "claude-opus-5-5")

	require.ErrorContains(t, err, "no rawMaxTokens")
}

// Claude guesses the window of a model it does not know, as a daemon's
// Claude older than the agent's may not know the agent's model.
func TestRepositoryProbeContextWindow_ReportsAModelClaudeDoesNotKnow(t *testing.T) {
	repo, _ := newContextWindowProbeRepository(
		t,
		initializeReply,
		contextUsageReply(`"rawMaxTokens":200000,"autocompactSource":"unknown-model",`),
	)

	_, err := repo.probeContextWindow(t.Context(), "claude-opus-5-5")

	require.ErrorContains(t, err, "claude does not know the model")
}

func TestRepositoryProbeContextWindow_ReportsStdoutEndingBeforeTheResponse(t *testing.T) {
	repo, _ := newContextWindowProbeRepository(t, initializeReply)

	_, err := repo.probeContextWindow(t.Context(), "claude-opus-5-5")

	require.ErrorIs(t, err, io.EOF)
	require.ErrorContains(t, err, "before answering get_context_usage")
}

// A probe that fails, as one of a model Claude does not know does, is a
// failure to the cache: the model keeps no window and is not probed again at
// once.
func TestRepositoryContextWindows_CountAFailedProbeAsAFailure(t *testing.T) {
	repo, _ := newContextWindowProbeRepository(
		t,
		initializeReply,
		contextUsageReply(`"rawMaxTokens":200000,"autocompactSource":"unknown-model",`),
	)
	probed := make(chan string, 1)
	repo.contextWindows.probed = func(model string) { probed <- model }

	require.Equal(t, 0, repo.contextWindows.lookup(t.Context(), "claude-opus-5-5"))
	require.Equal(t, "claude-opus-5-5", receiveWithin(t, probed))

	require.Equal(t, 0, repo.contextWindows.lookup(t.Context(), "claude-opus-5-5"))
	repo.contextWindows.mu.Lock()
	defer repo.contextWindows.mu.Unlock()
	entry := repo.contextWindows.entries["claude-opus-5-5"]
	require.False(t, entry.probing)
	require.False(t, entry.failedAt.IsZero())
}

// The first read after the conversation starts sets Claude probing, before
// any request has put anything in the context. Later reads give the context
// the model's window.
func TestReadConversationUsage_SetsTheWindowClaudeReportsForTheModel(t *testing.T) {
	repo, session := newContextWindowProbeRepository(
		t,
		initializeReply,
		contextUsageReply(knownModelUsage),
	)
	probed := make(chan string, 1)
	repo.contextWindows.probed = func(model string) { probed <- model }
	transcript := writeTranscript(t, `{"type":"user","message":{"role":"user","content":"fix the bug"}}`)
	conversation := []core.TaskProviderSession{{
		Provider:       core.ProviderClaude,
		TranscriptPath: transcript,
		Model:          "claude-opus-5-5",
	}}

	usage, err := repo.ReadConversationUsage(t.Context(), conversation)
	require.NoError(t, err)
	require.Equal(t, core.ConversationUsage{}, usage)
	require.Equal(t, "claude-opus-5-5", receiveWithin(t, probed))
	require.True(t, session.closed)

	appendTranscript(t, transcript, claudeAssistantEntry(false, "claude-opus-5-5", 3, 12000, 8000)+"\n")
	usage, err = repo.ReadConversationUsage(t.Context(), conversation)
	require.NoError(t, err)
	require.Equal(t, core.ConversationUsage{Context: &core.ContextUsage{Tokens: 20003, Window: 1000000}}, usage)
}
