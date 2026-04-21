package taskdaemon

import (
	"context"
	"net/http"

	"rig/internal/core"
)

type server struct {
	socketPath     string
	hookListenAddr string
	service        core.TaskService
	hookRoutes     []core.TaskDaemonHookRoute
	stop           func()
}

func (s *server) CreateTask(ctx context.Context, input core.CreateTaskInput) (*core.Task, error) {
	return s.service.CreateTask(ctx, input)
}

func (s *server) DeleteTask(ctx context.Context, taskID string) error {
	return s.service.DeleteTask(ctx, taskID)
}

func (s *server) ListTasks(ctx context.Context) ([]*core.Task, error) {
	return s.service.ListTasks(ctx)
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

	unixSocketServer := &unixSocketServer{
		socketPath: s.socketPath,
		frontend:   s,
		stop:       s.stop,
	}

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
	case serveErr := <-errCh:
		if serveErr != nil && !errorsIsHTTPServerClosed(serveErr) {
			_ = httpHookServer.Shutdown(context.Background())
			return serveErr
		}
	}

	_ = httpHookServer.Shutdown(context.Background())
	return nil
}

func errorsIsHTTPServerClosed(err error) bool {
	return err == http.ErrServerClosed
}
