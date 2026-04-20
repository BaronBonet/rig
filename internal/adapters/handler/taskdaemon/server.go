package taskdaemon

import (
	"context"
	"net/http"
	"rig/internal/core"
)

type Dependencies struct {
	Service    core.TaskService
	HookRoutes []HookRoute
	Stop       func()
}

type server struct {
	socketPath     string
	hookListenAddr string
	service        core.TaskService
	hookRoutes     []HookRoute
	stop           func()
}

func New(cfg Config, deps Dependencies) core.TaskFrontendServer {
	return &server{
		socketPath:     cfg.SocketPath,
		hookListenAddr: cfg.HookListenAddr,
		service:        deps.Service,
		hookRoutes:     deps.HookRoutes,
		stop:           deps.Stop,
	}
}

func (s *server) CreateTask(ctx context.Context, input core.CreateTaskInput) (*core.Task, error) {
	return s.service.CreateTask(ctx, input)
}

func (s *server) LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	return s.service.LatestTaskStatus(ctx, taskID)
}

func (s *server) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	return s.service.SubscribeTaskStatus(ctx, taskID)
}

func (s *server) Serve(ctx context.Context) error {
	httpHookListener, err := listenForHTTPHooks(s.hookListenAddr)
	if err != nil {
		return err
	}
	defer httpHookListener.Close()

	unixSocketServer := NewUnixSocketServer(UnixSocketServerConfig{
		SocketPath: s.socketPath,
		Frontend:   s,
		Stop:       s.stop,
	})

	httpHookServer := &http.Server{Handler: newHTTPHookServer(s.hookRoutes)}

	errCh := make(chan error, 2)
	go func() {
		errCh <- unixSocketServer.Serve(ctx)
	}()
	go func() {
		errCh <- httpHookServer.Serve(httpHookListener)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			_ = httpHookServer.Shutdown(context.Background())
			return err
		}
	}

	_ = httpHookServer.Shutdown(context.Background())
	return nil
}
