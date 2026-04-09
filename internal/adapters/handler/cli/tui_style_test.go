package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIconSet_NerdFontReturnsNerdGlyphs(t *testing.T) {
	icons := nerdFontIcons()
	require.Equal(t, "\uE725", icons.Branch)
	require.Equal(t, "\uF401", icons.Repo)
	require.Equal(t, "\uE726", icons.PROpen)
	require.Equal(t, "\uE727", icons.PRMerged)
	require.Equal(t, "\uF017", icons.Time)
	require.Equal(t, "\uF1E6", icons.Process)
	require.Equal(t, "\uF007", icons.Prompt)
	require.Equal(t, "\U000F06A9", icons.LLMOutput)
}

func TestIconSet_UnicodeFallbackReturnsEmoji(t *testing.T) {
	icons := unicodeFallbackIcons()
	require.Equal(t, "🌿", icons.Branch)
	require.Equal(t, "📁", icons.Repo)
	require.Equal(t, "◉", icons.PROpen)
	require.Equal(t, "✔", icons.PRMerged)
	require.Equal(t, "🕐", icons.Time)
	require.Equal(t, "🔌", icons.Process)
	require.Equal(t, "👤", icons.Prompt)
	require.Equal(t, "🤖", icons.LLMOutput)
}

func TestRenderShimmer_ReturnsStringWithSameVisibleLength(t *testing.T) {
	result := renderShimmer("Creating worktree...", 5)
	// Strip ANSI to get visible text
	visible := stripANSI(result)
	require.Equal(t, "Creating worktree...", visible)
}

func TestRenderShimmer_DifferentTicksProduceDifferentOutput(t *testing.T) {
	a := renderShimmer("Loading...", 0)
	b := renderShimmer("Loading...", 5)
	require.NotEqual(t, a, b)
}

func TestRenderShimmer_WrapsAroundAfterTextLength(t *testing.T) {
	text := "Hi"
	// tick well past the text length should wrap and still produce valid output
	result := renderShimmer(text, 100)
	visible := stripANSI(result)
	require.Equal(t, text, visible)
}
