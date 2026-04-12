package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
