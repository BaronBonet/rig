package taskdaemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/BaronBonet/rig/internal/core"
)

type server struct {
	service        core.TaskService
	socketPath     string
	hookListenAddr string
	stop           func()
	hookRoutes     []core.TaskDaemonHookRoute
}

func (s *server) GetTaskActivity(ctx context.Context, taskID string, limit int) ([]core.TaskActivityEvent, error) {
	if s.service == nil {
		return nil, fmt.Errorf("task service not configured")
	}

	return s.service.GetTaskActivity(ctx, taskID, limit)
}

func (s *server) GetTaskTokenUsage(ctx context.Context, taskID string) (*core.TaskTokenUsage, error) {
	if s.service == nil {
		return nil, fmt.Errorf("task service not configured")
	}

	return s.service.GetTaskTokenUsage(ctx, taskID)
}

func (s *server) ListRepoPullRequests(ctx context.Context, cwd string) ([]core.RepoPullRequest, error) {
	if s.service == nil {
		return nil, fmt.Errorf("task service not configured")
	}

	return s.service.ListRepoPullRequests(ctx, cwd)
}

func (s *server) PullRequestStatus(ctx context.Context, repoRoot string, branchName string) (*core.PRStatus, error) {
	if s.service == nil {
		return nil, fmt.Errorf("task service not configured")
	}

	return s.service.PullRequestStatus(ctx, repoRoot, branchName)
}

func (s *server) ReconnectTaskSession(ctx context.Context, taskID string) error {
	if s.service == nil {
		return fmt.Errorf("task service not configured")
	}

	return s.service.ReconnectTaskSession(ctx, taskID)
}

func (s *server) GetProviderSetup(ctx context.Context) (*core.ProviderSetup, error) {
	if s.service == nil {
		return nil, fmt.Errorf("task service not configured")
	}

	return s.service.GetProviderSetup(ctx)
}

func (s *server) SaveProviderSetup(ctx context.Context, setup core.ProviderSetup) error {
	if s.service == nil {
		return fmt.Errorf("task service not configured")
	}

	return s.service.SaveProviderSetup(ctx, setup)
}

func (s *server) DetectProviders(ctx context.Context) ([]core.ProviderDetection, error) {
	if s.service == nil {
		return nil, fmt.Errorf("task service not configured")
	}

	return s.service.DetectProviders(ctx)
}

func (s *server) SwitchTaskProvider(
	ctx context.Context,
	taskID string,
	provider core.Provider,
) (*core.Task, error) {
	if s.service == nil {
		return nil, fmt.Errorf("task service not configured")
	}

	return s.service.SwitchTaskProvider(ctx, taskID, provider)
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
			case events <- core.TaskCreateEvent{Err: err, Task: task}:
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

func (s *server) RetryTaskCreationStream(
	ctx context.Context,
	taskID string,
) (<-chan core.TaskCreateEvent, error) {
	events := make(chan core.TaskCreateEvent, 8)

	go func() {
		defer close(events)

		task, err := s.service.RetryTaskCreationWithProgress(ctx, taskID, taskCreateEventReporter{
			ctx:    ctx,
			events: events,
		})
		if err != nil {
			select {
			case <-ctx.Done():
			case events <- core.TaskCreateEvent{Err: err, Task: task}:
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
		backend:    s,
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
