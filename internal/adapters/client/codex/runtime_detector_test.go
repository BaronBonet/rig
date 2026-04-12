package codex

import (
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRuntimeDetector_Detect_PrefersBusyMarkerOverPromptMarker(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		ForegroundCommand: "codex",
		Content:           "› review current changes\nWorking (26s • esc to interrupt)",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForPromptWithoutRecentOutput(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "› review current changes\n  gpt-5.4 high · 82% left",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForPromptEvenWithRecentOutput(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "› review current changes\n  gpt-5.4 high · 82% left",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForCodexAliasCommand(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex-aarch64-a",
		Content:           "› review current changes\n  gpt-5.4 high · 82% left",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForANSIPrompt(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex-aarch64-a",
		Content:           "\x1b[1m›\x1b[0m\x1b[48;2;53;54;64m \x1b[2mImplement {feature}\x1b[0m\n\x1b[2m  gpt-5.4 high · 70% left\x1b[0m\n",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsFinishedWhenPaneReturnsToShellAfterReusedBinding(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		HadAgentBinding:   true,
		ForegroundCommand: "zsh",
		Content:           "done\n",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateFinished, state)
}

func TestRuntimeDetector_Detect_ReturnsEmptyForFirstShellObservation(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "zsh",
		Content:           "done\n",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNone, state)
}

func TestRuntimeDetector_Detect_ReturnsEmptyForShellOnlyRepeatedObservation(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	first := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "zsh",
		Content:           "done\n",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})
	second := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "zsh",
		Content:           "done\n",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 1, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNone, first)
	require.Equal(t, core.RuntimeStateNone, second)
}

func TestRuntimeDetector_Detect_ReturnsFinishedOnlyAfterPriorCodexOwnership(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		HadAgentBinding:   true,
		ForegroundCommand: "zsh",
		Content:           "done\n",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 1, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateFinished, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForContinuePrompt(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "Continue?\n",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsEmptyForUnclassifiedSnapshot(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "some unrelated output\n",
		HadAgentBinding:   false,
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 40, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNone, state)
}
