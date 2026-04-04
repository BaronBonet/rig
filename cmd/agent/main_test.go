package main

import (
	"testing"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRuntimeService_NewServiceConstructs(t *testing.T) {
	svc := &runtimeService{cfg: core.DefaultConfig(), runner: execx.ExecRunner{}}

	service, err := svc.newService(false)
	require.NoError(t, err)
	require.NotNil(t, service)
}
