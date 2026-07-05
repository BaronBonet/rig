package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProgram_CreatesBubbleTeaProgram(t *testing.T) {
	program := NewProgram(newFrontendHarness().mock, "/tmp/repo", "1.2.3")
	require.NotNil(t, program)
}
