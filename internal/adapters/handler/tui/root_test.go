package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProgram_CreatesBubbleTeaProgram(t *testing.T) {
	program := NewProgram(newFrontendHarness().mock, "/tmp/repo")
	require.NotNil(t, program)
}
