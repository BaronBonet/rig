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

type httpHookServerConfig struct {
	Service core.TaskService
	Tasks   core.TaskRepository
	Now     func() time.Time
}

type httpHookServer struct {
	service core.TaskService
	tasks   core.TaskRepository
	now     func() time.Time
}

func newHTTPHookServer(cfg httpHookServerConfig) *httpHookServer {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &httpHookServer{
		service: cfg.Service,
		tasks:   cfg.Tasks,
		now:     cfg.Now,
	}
}

func listenForHTTPHooks(addr string) (net.Listener, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("task daemon hook listen addr not configured")
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for task daemon hook ingestion: %w", err)
	}

	return listener, nil
}

func (s *httpHookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	input := codexhooks.DecodeHookEventInput(s.now, r.Header.Get("X-Codex-Hook-Event"), mustReadAll(r))
	update, err := s.codexHookToTaskStatus(r.Context(), input)
	if err != nil && !errors.Is(err, core.ErrUnmanagedHookEvent) {
		http.Error(w, "publish hook event: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if update != nil {
		if err := s.service.PublishTaskStatus(r.Context(), *update); err != nil {
			http.Error(w, "publish task status: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *httpHookServer) codexHookToTaskStatus(ctx context.Context, input core.HookEventInput) (*core.TaskStatusUpdate, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		resolvedTaskID, err := s.resolveTaskID(ctx, strings.TrimSpace(input.Cwd))
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
		observedAt = s.now().UTC()
	}

	return &core.TaskStatusUpdate{
		TaskID:       taskID,
		Provider:     core.AgentProviderCodex,
		Phase:        phase,
		RawEventName: eventName,
		ObservedAt:   observedAt,
	}, nil
}

func (s *httpHookServer) resolveTaskID(ctx context.Context, cwd string) (string, error) {
	if s.tasks == nil || cwd == "" {
		return "", core.ErrUnmanagedHookEvent
	}

	tasks, err := s.tasks.ListTasks(ctx)
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
