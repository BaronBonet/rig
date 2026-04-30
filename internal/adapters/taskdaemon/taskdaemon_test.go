package taskdaemon

import (
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestNew_ReturnsCoreTaskDaemon(t *testing.T) {
	adapter := New(Config{})
	require.NotNil(t, adapter)
	require.NotNil(t, adapter.Frontend())
}

var _ core.TaskDaemon = New(Config{})
