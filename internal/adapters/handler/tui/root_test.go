package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProgram_CreatesBubbleTeaProgram(t *testing.T) {
	program := NewProgram(newStubFrontend())
	require.NotNil(t, program)
}
