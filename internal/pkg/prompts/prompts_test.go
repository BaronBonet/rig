package prompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSuggestTaskPrompt_IsNonEmpty(t *testing.T) {
	require.NotEmpty(t, SuggestTaskPrompt)
}

func TestSuggestTaskPrompt_ContainsBranchTypeInstruction(t *testing.T) {
	require.True(t, strings.Contains(SuggestTaskPrompt, "branch_type"))
}

func TestSuggestTaskPrompt_ContainsNameInstruction(t *testing.T) {
	require.True(t, strings.Contains(SuggestTaskPrompt, "name"))
}
