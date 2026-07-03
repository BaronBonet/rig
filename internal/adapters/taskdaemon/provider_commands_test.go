package taskdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSocketUnaryCommandsRegisterProviderCommands(t *testing.T) {
	t.Parallel()

	require.Contains(t, socketUnaryCommands, socketCommandGetProviderSetup)
	require.Contains(t, socketUnaryCommands, socketCommandSaveProviderSetup)
	require.Contains(t, socketUnaryCommands, socketCommandDetectProviders)
	require.Contains(t, socketUnaryCommands, socketCommandSwitchTaskProvider)
}

func TestUnixSocketServer_ProviderSetupCommandsCallTaskService(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	setup := &core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderCodex,
	}
	svc := core.NewMockTaskService(t)
	svc.EXPECT().GetProviderSetup(mock.Anything).Return(setup, nil)
	svc.EXPECT().SaveProviderSetup(mock.Anything, *setup).Return(nil)
	svc.EXPECT().DetectProviders(mock.Anything).Return([]core.ProviderDetection{
		{Provider: core.ProviderCodex, Ready: true},
		{Provider: core.ProviderClaude, Ready: false, Detail: "claude binary not found"},
	}, nil)
	svc.EXPECT().SwitchTaskProvider(mock.Anything, "task-1", core.ProviderClaude).Return(&core.Task{
		ID:       "task-1",
		Provider: core.ProviderClaude,
	}, nil)

	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	frontend := New(Config{SocketPath: socketPath}).Frontend()

	gotSetup, err := frontend.GetProviderSetup(context.Background())
	require.NoError(t, err)
	require.Equal(t, setup, gotSetup)

	require.NoError(t, frontend.SaveProviderSetup(context.Background(), *setup))

	detections, err := frontend.DetectProviders(context.Background())
	require.NoError(t, err)
	require.Len(t, detections, 2)
	require.True(t, detections[0].Ready)
	require.Equal(t, "claude binary not found", detections[1].Detail)

	task, err := frontend.SwitchTaskProvider(context.Background(), "task-1", core.ProviderClaude)
	require.NoError(t, err)
	require.Equal(t, core.ProviderClaude, task.Provider)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_GetProviderSetupReturnsNilSetupBeforeSetupHasRun(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().GetProviderSetup(mock.Anything).Return(nil, nil)

	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	frontend := New(Config{SocketPath: socketPath}).Frontend()

	setup, err := frontend.GetProviderSetup(context.Background())
	require.NoError(t, err)
	require.Nil(t, setup)

	cancel()
	require.NoError(t, <-errCh)
}

func TestUnixSocketServer_SwitchTaskProviderSurfacesServiceErrors(t *testing.T) {
	t.Parallel()

	socketPath := serverTestSocketPath(t)
	svc := core.NewMockTaskService(t)
	svc.EXPECT().SwitchTaskProvider(mock.Anything, "task-1", core.ProviderClaude).
		Return(nil, errors.New("provider session is still running"))

	adapter := New(Config{
		SocketPath:     socketPath,
		HookListenAddr: "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Serve(ctx, svc, nil, nil)
	}()
	waitForUnixSocketServer(t, socketPath)

	frontend := New(Config{SocketPath: socketPath}).Frontend()

	task, err := frontend.SwitchTaskProvider(context.Background(), "task-1", core.ProviderClaude)
	require.Nil(t, task)
	require.ErrorContains(t, err, "provider session is still running")

	cancel()
	require.NoError(t, <-errCh)
}

func TestFrontend_SwitchTaskProviderSendsTaskIDAndProvider(t *testing.T) {
	t.Parallel()

	socketPath := frontendTestSocketPath(t)
	requestCh := make(chan socketRequest, 1)
	serverErrCh := serveOneShotFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
		requestCh <- req
		return encoder.Encode(socketEnvelope{
			Type: socketEnvelopeTaskProviderSwitched,
			OK:   true,
			Task: &core.Task{ID: "task-1", Provider: core.ProviderClaude},
		})
	})

	frontend := New(Config{SocketPath: socketPath}).Frontend()

	task, err := frontend.SwitchTaskProvider(t.Context(), "task-1", core.ProviderClaude)
	require.NoError(t, err)
	require.Equal(t, core.ProviderClaude, task.Provider)
	require.Equal(t, socketRequest{
		Command:  socketCommandSwitchTaskProvider,
		TaskID:   "task-1",
		Provider: core.ProviderClaude,
	}, <-requestCh)
	require.NoError(t, <-serverErrCh)
}
