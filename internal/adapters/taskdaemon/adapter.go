package taskdaemon

import (
	"context"
	"fmt"
	"rig/internal/core"
)

type Adapter struct {
	cfg      Config
	frontend core.TaskFrontend
}

func New(cfg Config) *Adapter {
	return &Adapter{
		cfg:      cfg,
		frontend: &frontend{socketPath: cfg.SocketPath},
	}
}

func (a *Adapter) Frontend() core.TaskFrontend {
	if a == nil {
		return nil
	}

	return a.frontend
}

func (a *Adapter) EnsureRunning(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("task daemon adapter not configured")
	}

	return ensureRunning(ctx, a.cfg)
}

func (a *Adapter) Restart(ctx context.Context) error {
	if a == nil {
		return fmt.Errorf("task daemon adapter not configured")
	}

	return restartDaemon(ctx, a.cfg)
}

func (a *Adapter) Serve(
	ctx context.Context,
	service core.TaskService,
	hookRoutes []HookRoute,
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
