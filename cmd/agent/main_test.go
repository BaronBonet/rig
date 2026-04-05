package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildDependencies_WiresSharedCodexRuntimeMonitor(t *testing.T) {
	deps, err := buildDependencies()
	require.NoError(t, err)

	svc, ok := deps.Service.(*runtimeService)
	require.True(t, ok)
	require.NotNil(t, svc.runtimeMonitor)
	require.Contains(t, svc.runtimeDetectors, "codex")
	require.Contains(t, svc.runtimeDetectors, "claude")

	service1, err := svc.newService(false)
	require.NoError(t, err)
	require.NotNil(t, service1)

	service2, err := svc.newService(false)
	require.NoError(t, err)
	require.NotNil(t, service2)
}
