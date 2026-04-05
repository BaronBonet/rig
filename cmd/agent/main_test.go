package main

import (
	"reflect"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestBuildDependencies_WiresSharedCodexRuntimeMonitor(t *testing.T) {
	deps, err := buildDependencies()
	require.NoError(t, err)

	svc, ok := deps.Service.(*runtimeService)
	require.True(t, ok)
	require.NotNil(t, svc.runtimeMonitor)
	require.Contains(t, svc.runtimeDetectors, "codex")
	require.NotContains(t, svc.runtimeDetectors, "claude")

	service1, err := svc.newService(false)
	require.NoError(t, err)
	service2, err := svc.newService(false)
	require.NoError(t, err)

	require.Equal(t, runtimeMonitorPtr(t, svc.runtimeMonitor), runtimeMonitorPtrFromService(t, service1))
	require.Equal(t, runtimeMonitorPtr(t, svc.runtimeMonitor), runtimeMonitorPtrFromService(t, service2))
}

func runtimeMonitorPtr(t *testing.T, monitor core.RuntimeMonitor) uintptr {
	t.Helper()
	value := reflect.ValueOf(monitor)
	require.True(t, value.IsValid())
	require.Equal(t, reflect.Ptr, value.Kind())
	return value.Pointer()
}

func runtimeMonitorPtrFromService(t *testing.T, service *core.Service) uintptr {
	t.Helper()
	value := reflect.ValueOf(service).Elem().FieldByName("runtimeMonitor")
	require.True(t, value.IsValid())
	require.False(t, value.IsNil())
	return value.Elem().Pointer()
}
