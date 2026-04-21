package taskdaemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"rig/internal/core"
)

type server struct {
	service        core.TaskService
	socketPath     string
	hookListenAddr string
	stop           func()
	hookRoutes     []core.TaskDaemonHookRoute
}

func (s *server) OpenTaskSession(context.Context, *core.Task) error {
	return fmt.Errorf("open task session unsupported on daemon server")
}

type taskCreateEventReporter struct {
	ctx    context.Context
	events chan<- core.TaskCreateEvent
}

func (r taskCreateEventReporter) ReportTaskCreateProgress(step core.TaskCreateProgressStep) {
	select {
	case <-r.ctx.Done():
		return
	case r.events <- core.TaskCreateEvent{Progress: &core.TaskCreateProgressEvent{Step: step}}:
	}
}

func (s *server) CreateTaskStream(
	ctx context.Context,
	input core.CreateTaskInput,
) (<-chan core.TaskCreateEvent, error) {
	events := make(chan core.TaskCreateEvent, 8)

	go func() {
		defer close(events)

		task, err := s.service.CreateTaskWithProgress(ctx, input, taskCreateEventReporter{
			ctx:    ctx,
			events: events,
		})
		if err != nil {
			select {
			case <-ctx.Done():
			case events <- core.TaskCreateEvent{Err: err}:
			}
			return
		}

		select {
		case <-ctx.Done():
		case events <- core.TaskCreateEvent{Task: task}:
		}
	}()

	return events, nil
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
	httpHookListener, err := listenForHTTPHooks(ctx, s.hookListenAddr)
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
	return errors.Is(err, http.ErrServerClosed)
}
