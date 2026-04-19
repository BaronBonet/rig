package taskdaemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	codexhooks "rig/internal/adapters/observability/codexhooks"
	"rig/internal/core"
)

type Config struct {
	SocketPath     string
	HookListenAddr string
	Service        core.TaskService
	Tasks          core.TaskRepository
	Now            func() time.Time
	HookListener   net.Listener
	Fingerprint    string
	Stop           func()
}

type Server interface {
	core.TaskFrontend
	Serve(context.Context) error
}

type daemon struct {
	socketPath     string
	hookListenAddr string
	service        core.TaskService
	tasks          core.TaskRepository
	now            func() time.Time
	hookListener   net.Listener
	fingerprint    string
	stop           func()
}

func New(cfg Config) Server {
	return newDaemon(cfg)
}

func newDaemon(cfg Config) *daemon {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &daemon{
		socketPath:     cfg.SocketPath,
		hookListenAddr: cfg.HookListenAddr,
		service:        cfg.Service,
		tasks:          cfg.Tasks,
		now:            cfg.Now,
		hookListener:   cfg.HookListener,
		fingerprint:    cfg.Fingerprint,
		stop:           cfg.Stop,
	}
}

func (d *daemon) CreateTask(ctx context.Context, input core.CreateTaskInput) (*core.Task, error) {
	return d.service.CreateTask(ctx, input)
}

func (d *daemon) LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	return d.service.LatestTaskStatus(ctx, taskID)
}

func (d *daemon) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	return d.service.SubscribeTaskStatus(ctx, taskID)
}

func (d *daemon) Serve(ctx context.Context) error {
	if d.socketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}
	if d.hookListenAddr == "" && d.hookListener == nil {
		return fmt.Errorf("task daemon hook listen addr not configured")
	}
	if d.service == nil {
		return fmt.Errorf("task daemon task service not configured")
	}

	httpHookListener := d.hookListener
	var err error
	if httpHookListener == nil {
		httpHookListener, err = net.Listen("tcp", d.hookListenAddr)
		if err != nil {
			return fmt.Errorf("listen for task daemon hook ingestion: %w", err)
		}
	}
	defer httpHookListener.Close()

	unixSocketServer := NewUnixSocketServer(UnixSocketServerConfig{
		SocketPath:  d.socketPath,
		Frontend:    d,
		Fingerprint: d.fingerprint,
		Stop:        d.stop,
	})

	httpHookMux := http.NewServeMux()
	httpHookMux.Handle("/codex-hook", d.codexHookHandler())
	httpHookMux.Handle("/hook", d.codexHookHandler())
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

func (d *daemon) codexHookHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		input := codexhooks.DecodeHookEventInput(d.now, r.Header.Get("X-Codex-Hook-Event"), mustReadAll(r))
		update, err := d.codexHookToTaskStatus(r.Context(), input)
		if err != nil && !errors.Is(err, core.ErrUnmanagedHookEvent) {
			http.Error(w, "publish hook event: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if update != nil {
			if err := d.service.PublishTaskStatus(r.Context(), *update); err != nil {
				http.Error(w, "publish task status: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusAccepted)
	})
}

func (d *daemon) codexHookToTaskStatus(ctx context.Context, input core.HookEventInput) (*core.TaskStatusUpdate, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		resolvedTaskID, err := d.resolveTaskID(ctx, strings.TrimSpace(input.Cwd))
		if err != nil {
			return nil, err
		}
		taskID = resolvedTaskID
	}

	eventName := strings.TrimSpace(input.EventName)
	if eventName == "" {
		return nil, core.ErrUnmanagedHookEvent
	}

	var phase core.TaskStatusPhase
	switch eventName {
	case "SessionStart":
		phase = core.TaskStatusPhaseStarting
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		phase = core.TaskStatusPhaseWorking
	case "Stop":
		phase = core.TaskStatusPhaseWaitingForInput
	default:
		return nil, nil
	}

	observedAt := input.OccurredAt
	if observedAt.IsZero() {
		observedAt = d.now().UTC()
	}

	return &core.TaskStatusUpdate{
		TaskID:       taskID,
		Provider:     core.AgentProviderCodex,
		Phase:        phase,
		RawEventName: eventName,
		ObservedAt:   observedAt,
	}, nil
}

func (d *daemon) resolveTaskID(ctx context.Context, cwd string) (string, error) {
	if d.tasks == nil || cwd == "" {
		return "", core.ErrUnmanagedHookEvent
	}

	tasks, err := d.tasks.ListTasks(ctx)
	if err != nil {
		return "", fmt.Errorf("list tasks for hook resolution: %w", err)
	}

	for _, task := range tasks {
		if task != nil && strings.TrimSpace(task.WorktreePath) == cwd {
			return strings.TrimSpace(task.ID), nil
		}
	}

	return "", core.ErrUnmanagedHookEvent
}
