package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"rig/internal/core"
	"strings"
	"sync"
)

type UnixSocketServerConfig struct {
	SocketPath string
	Frontend   core.TaskFrontend
	Stop       func()
}

type UnixSocketServer struct {
	socketPath string
	frontend   core.TaskFrontend
	stop       func()
}

type socketRequest struct {
	Command string                `json:"command"`
	TaskID  string                `json:"task_id,omitempty"`
	Input   *core.CreateTaskInput `json:"input,omitempty"`
}

type socketEnvelope struct {
	Type   string                 `json:"type"`
	OK     bool                   `json:"ok,omitempty"`
	Error  string                 `json:"error,omitempty"`
	Task   *core.Task             `json:"task,omitempty"`
	Update *core.TaskStatusUpdate `json:"update,omitempty"`
}

func NewUnixSocketServer(cfg UnixSocketServerConfig) *UnixSocketServer {
	return &UnixSocketServer{
		socketPath: cfg.SocketPath,
		frontend:   cfg.Frontend,
		stop:       cfg.Stop,
	}
}

func (s *UnixSocketServer) Serve(ctx context.Context) error {
	if s.socketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}
	if s.frontend == nil {
		return fmt.Errorf("task daemon frontend not configured")
	}

	// Unix sockets are regular filesystem entries, so the parent directory must
	// exist before we can bind the socket file.
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return fmt.Errorf("prepare task daemon socket directory: %w", err)
	}
	// A previous crash can leave the socket path behind; remove it so this
	// process can bind a fresh listener.
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
		conn, err := listener.Accept()
		if err != nil {
			// Closing the listener during shutdown causes Accept to return an
			// error; treat that as a normal exit instead of a server failure.
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}

		go s.handleConn(ctx, conn)
	}
}

func (s *UnixSocketServer) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

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
	case "create_task":
		s.handleCreateTask(ctx, encoder, req)
	case "latest_task_status":
		s.handleLatestTaskStatus(ctx, encoder, req)
	case "subscribe_task_status":
		s.handleSubscribeTaskStatus(ctx, encoder, req)
	case "stop":
		_ = encoder.Encode(socketEnvelope{Type: "stopping", OK: true})
		if s.stop != nil {
			s.stop()
		}
	default:
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: fmt.Sprintf("unsupported command %q", req.Command)})
	}
}

func (s *UnixSocketServer) handleCreateTask(ctx context.Context, encoder *json.Encoder, req socketRequest) {
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

func (s *UnixSocketServer) handleLatestTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	update, err := s.frontend.LatestTaskStatus(ctx, strings.TrimSpace(req.TaskID))
	if err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	_ = encoder.Encode(socketEnvelope{Type: "task_status_snapshot", OK: true, Update: update})
}

func (s *UnixSocketServer) handleSubscribeTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: "subscribe_task_status task_id required"})
		return
	}

	if err := encoder.Encode(socketEnvelope{Type: "subscribed", OK: true}); err != nil {
		return
	}

	updates, err := s.frontend.SubscribeTaskStatus(ctx, taskID)
	if err != nil {
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

func probeSocketHealth(ctx context.Context, socketPath string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	var (
		writeErr error
		readErr  error
		resp     socketEnvelope
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		writeErr = json.NewEncoder(conn).Encode(socketRequest{Command: "health"})
	}()
	go func() {
		defer wg.Done()
		readErr = json.NewDecoder(bufio.NewReader(conn)).Decode(&resp)
	}()
	wg.Wait()

	if writeErr != nil {
		return writeErr
	}
	if readErr != nil {
		return readErr
	}
	if resp.Type != "health" || !resp.OK {
		return fmt.Errorf("task daemon unhealthy")
	}

	return nil
}

func dialSocketHealth(ctx context.Context, socketPath string) error {
	return probeSocketHealth(ctx, socketPath)
}

func mustReadAll(r *http.Request) []byte {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	return body
}
