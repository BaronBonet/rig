package taskdaemon

import (
	"context"
	"net/http"
	"rig/internal/core"
)

type Dependencies struct {
	Service core.TaskService
	Tasks   core.TaskRepository
	Stop    func()
}

type Server struct {
	socketPath     string
	hookListenAddr string
	service        core.TaskService
	stop           func()
	httpHooks      *httpHookServer
}

func New(cfg Config, deps Dependencies) *Server {
	return &Server{
		socketPath:     cfg.SocketPath,
		hookListenAddr: cfg.HookListenAddr,
		service:        deps.Service,
		stop:           deps.Stop,
		httpHooks: newHTTPHookServer(httpHookServerConfig{
			Service: deps.Service,
			Tasks:   deps.Tasks,
		}),
	}
}

func (s *Server) CreateTask(ctx context.Context, input core.CreateTaskInput) (*core.Task, error) {
	return s.service.CreateTask(ctx, input)
}

func (s *Server) LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	return s.service.LatestTaskStatus(ctx, taskID)
}

func (s *Server) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	return s.service.SubscribeTaskStatus(ctx, taskID)
}

func (s *Server) Serve(ctx context.Context) error {
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

	httpHookMux := http.NewServeMux()
	httpHookMux.Handle("/codex-hook", s.httpHooks)
	httpHookMux.Handle("/hook", s.httpHooks)
	httpHookServer := &http.Server{Handler: httpHookMux}

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
