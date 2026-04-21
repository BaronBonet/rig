package taskdaemon

import (
	"context"
	"fmt"

	tmuxsession "rig/internal/adapters/client/tmuxsession"
	"rig/internal/core"
	"rig/internal/pkg/subprocess"
)

type adapter struct {
	frontend core.TaskFrontend
	cfg      Config
}

func New(cfg Config) core.TaskDaemon {
	return &adapter{
		cfg: cfg,
		frontend: &frontend{
			socketPath: cfg.SocketPath,
			sessions:   tmuxsession.New(subprocess.ExecRunner{}),
		},
	}
}

func (a *adapter) Frontend() core.TaskFrontend {
	if a == nil {
		return nil
	}

	return a.frontend
}

func (a *adapter) EnsureRunning(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("task daemon adapter not configured")
	}

	return ensureRunning(ctx, a.cfg)
}

func (a *adapter) Restart(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("task daemon adapter not configured")
	}

	return restartDaemon(ctx, a.cfg)
}

func (a *adapter) Serve(
	ctx context.Context,
	service core.TaskService,
	hookRoutes []core.TaskDaemonHookRoute,
	stop func(),
) error {
	if a == nil {
		return fmt.Errorf("task daemon adapter not configured")
	}

	server := &server{
		socketPath:     a.cfg.SocketPath,
		hookListenAddr: a.cfg.HookListenAddr,
		service:        service,
		hookRoutes:     hookRoutes,
		stop:           stop,
	}

	return server.Serve(ctx)
}
