package claude

import (
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRuntimeDetector_Detect_ReturnsRunningForBusyMarkerInTail(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		ForegroundCommand: "claude",
		Content:           "some earlier output\n  esc to interrupt",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
}

func TestRuntimeDetector_Detect_IgnoresToolMarkerWithoutBusyIndicator(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	// ⏺ alone is not a busy marker — it persists in output history
	state := detector.Detect(core.RuntimeSnapshot{
		ForegroundCommand: "claude",
		Content:           "⏺ Write(src/main.go)\n  some content here",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNone, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForPrompt(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content:           "Some output\n❯ ",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForPromptWithText(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content:           "Done editing file.\n❯ follow up question here",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForInsertMode(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	// Claude Code shows -- INSERT -- when waiting for user input
	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content:           "❯ can\n────────────\n  -- INSERT -- ⏵⏵ accept edits on (shift+tab to cycle)",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForConfirmation(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content:           "Allow this action? (y/n)",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsRunningForRecentOutput(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content:           "some output being generated...",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
}

func TestRuntimeDetector_Detect_ReturnsFinishedWhenShellAfterAgentBinding(t *testing.T) {
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

func TestRuntimeDetector_Detect_PrefersBusyMarkerOverShellForegroundCommand(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		HadAgentBinding:   true,
		ForegroundCommand: "bash",
		Content:           "⏺ Bash(go test ./...)\n  esc to interrupt",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
}

func TestRuntimeDetector_Detect_ReturnsRunningForToolForegroundCommandWithBusyMarker(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		HadAgentBinding:   true,
		ForegroundCommand: "go",
		Content:           "⏺ Bash(go test ./...)\n  esc to interrupt",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
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

func TestRuntimeDetector_Detect_ReturnsEmptyForUnclassifiedSnapshot(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content:           "some unrelated output\n",
		HadAgentBinding:   false,
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 40, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNone, state)
}

func TestRuntimeDetector_Detect_PromptTakesPriorityOverBusyMarkerInTail(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		ForegroundCommand: "claude",
		Content:           "  esc to interrupt\nDone.\n❯ ",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_LiveProgressFooterBeatsPrompt(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%48",
		HadAgentBinding:   true,
		ForegroundCommand: "claude-2.1.92",
		Content: "⏺ Update(internal/adapters/handler/cli/tui_model.go)\n" +
			"⏺ Now I'll add spinner message forwarding and update the busy message at each step.\n" +
			"· Unravelling… (1m 9s · ↓ 1.1k tokens)\n" +
			"❯ \n" +
			"  -- INSERT -- ⏵⏵ accept edits on (shift+tab to cycle)",
		ObservedAt:   time.Date(2026, 4, 6, 10, 7, 0, 0, time.UTC),
		LastOutputAt: time.Date(2026, 4, 6, 10, 6, 40, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
}

func TestRuntimeDetector_Detect_NeedsInputEvenWithHistoricalToolMarkers(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	// Simulates real Claude output: ⏺ markers from completed tools
	// followed by the input prompt at the bottom
	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content:           "⏺ Read(src/main.go)\n  contents...\n⏺ Write(src/main.go)\n  updated\n* Churned for 10m 39s\n\n❯ ",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_HandlesANSIEscapesAroundPrompt(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content:           "\x1b[1m❯\x1b[0m \x1b[2mtype here\x1b[0m\n",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_HandlesClaudeVersionedBinary(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude-2.1.92",
		Content:           "Working on task\n  esc to interrupt",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
}

func TestRuntimeDetector_Detect_RecognizesNodeAsClaudeRuntime(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "node",
		Content:           "⏺ Read(src/main.go)\n  contents...\n* Crunched for 2m 37s\n\n❯ ",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_NodeRunningWithRecentOutput(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "node",
		Content:           "generating output...",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
}

func TestRuntimeDetector_Detect_RealWorldIdlePaneContent(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	// Real captured pane content when Claude is waiting for input
	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%42",
		ForegroundCommand: "claude-2.1.92",
		Content:           "⏺ Reading file src/main.go\n  +14 lines (ctrl+o to expand)\n\nAll tests pass.\n\n* Churned for 10m 39s\n\n❯ can\n────────────────────────────\n  -- INSERT -- ⏵⏵ accept edits on (shift+tab to cycle)",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForPermissionPrompt(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	// Real Claude Code permission prompt. The selection indicator may not be
	// the ❯ (U+276F) character, so we test with a generic ">" to simulate
	// what tmux may actually capture. The footer "Esc to cancel" should be
	// the reliable detection signal.
	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "claude",
		Content: "Bash command\n\n" +
			"    git log --all --oneline --grep=\"branch\\|type\\|feat\\|fix\" -i | head -20\n" +
			"    Run shell command\n\n" +
			"Do you want to proceed?\n" +
			"> 1. Yes\n" +
			"  2. Yes, and don't ask again for: git log:*\n" +
			"  3. No\n\n" +
			"Esc to cancel \u00b7 Tab to amend \u00b7 ctrl+e to explain",
		ObservedAt:   time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt: time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsNeedsInputForWorkspaceTrustPrompt(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	// Workspace trust permission prompt with "Enter to confirm" footer
	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "node",
		Content: "Do you trust the files in this folder?\n" +
			"> 1. Yes, proceed\n" +
			"  2. No, exit\n\n" +
			"Enter to confirm \u00b7 Esc to cancel",
		ObservedAt:   time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt: time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_PermissionPromptNotConfusedWithBusy(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	// "Esc to cancel" must not be confused with "esc to interrupt" (busy marker).
	// When HadAgentBinding is true the busy-marker check runs first,
	// so ensure permission footers are not misclassified as busy.
	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		HadAgentBinding:   true,
		ForegroundCommand: "node",
		Content: "Do you want to proceed?\n" +
			"> 1. Yes\n" +
			"  2. Yes, and don't ask again for: git log:*\n" +
			"  3. No\n\n" +
			"Esc to cancel \u00b7 Tab to amend \u00b7 ctrl+e to explain",
		ObservedAt:   time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt: time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

