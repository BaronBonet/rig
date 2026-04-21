package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"rig/internal/core"
)

type unixSocketServer struct {
	socketPath string
	frontend   core.TaskFrontend
	stop       func()
}

func (s *unixSocketServer) Serve(ctx context.Context) error {
	if s.socketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}
	if s.frontend == nil {
		return fmt.Errorf("task daemon frontend not configured")
	}

	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return fmt.Errorf("prepare task daemon socket directory: %w", err)
	}
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale task daemon socket: %w", err)
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on task daemon socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(s.socketPath)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}
			return acceptErr
		}

		go s.handleConn(ctx, conn)
	}
}

func (s *unixSocketServer) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	decoder := json.NewDecoder(bufio.NewReader(conn))
	encoder := json.NewEncoder(conn)

	var req socketRequest
	if err := decoder.Decode(&req); err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	switch req.Command {
	case "health":
		_ = encoder.Encode(socketEnvelope{Type: "health", OK: true})
	case "frontend_build_version":
		_ = encoder.Encode(socketEnvelope{
			Type:    "frontend_build_version",
			OK:      true,
			Version: currentFrontendBuildVersion,
		})
	case "create_task":
		s.handleCreateTask(ctx, encoder, req)
	case "delete_task":
		s.handleDeleteTask(connCtx, encoder, req)
	case "list_tasks":
		s.handleListTasks(ctx, encoder)
	case "latest_task_status":
		s.handleLatestTaskStatus(connCtx, encoder, req)
	case "subscribe_task_status":
		go cancelOnConnClose(conn, cancel)
		s.handleSubscribeTaskStatus(connCtx, encoder, req)
	case "stop":
		_ = encoder.Encode(socketEnvelope{Type: "stopping", OK: true})
		if s.stop != nil {
			s.stop()
		}
	default:
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: fmt.Sprintf("unsupported command %q", req.Command)})
	}
}

func (s *unixSocketServer) handleCreateTask(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	if req.Input == nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: "create_task input required"})
		return
	}

	task, err := s.frontend.CreateTask(ctx, *req.Input)
	if err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	_ = encoder.Encode(socketEnvelope{Type: "task_created", OK: true, Task: task})
}

func (s *unixSocketServer) handleDeleteTask(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: "delete_task task_id required"})
		return
	}

	if err := s.frontend.DeleteTask(ctx, taskID); err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	_ = encoder.Encode(socketEnvelope{Type: "task_deleted", OK: true})
}

func (s *unixSocketServer) handleListTasks(ctx context.Context, encoder *json.Encoder) {
	tasks, err := s.frontend.ListTasks(ctx)
	if err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	_ = encoder.Encode(socketEnvelope{Type: "tasks_list", OK: true, Tasks: tasks})
}

func (s *unixSocketServer) handleLatestTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	update, err := s.frontend.LatestTaskStatus(ctx, strings.TrimSpace(req.TaskID))
	if err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	_ = encoder.Encode(socketEnvelope{Type: "task_status_snapshot", OK: true, Update: update})
}

func (s *unixSocketServer) handleSubscribeTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: "subscribe_task_status task_id required"})
		return
	}

	updates, err := s.frontend.SubscribeTaskStatus(ctx, taskID)
	if err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	if err := encoder.Encode(socketEnvelope{Type: "subscribed", OK: true}); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if err := encoder.Encode(socketEnvelope{Type: "task_status_update", Update: &update}); err != nil {
				return
			}
		}
	}
}

func cancelOnConnClose(conn net.Conn, cancel context.CancelFunc) {
	var buf [1]byte
	_, _ = conn.Read(buf[:])
	cancel()
}
