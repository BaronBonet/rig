package statusdaemon

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
	"strings"
	"sync"

	"rig/internal/core"
)

type SocketServerConfig struct {
	SocketPath  string
	Daemon      *Daemon
	Fingerprint string
	Stop        func()
}

type SocketServer struct {
	socketPath  string
	daemon      *Daemon
	fingerprint string
	stop        func()
}

type HealthStatus struct {
	Fingerprint string
}

type socketRequest struct {
	Command string                `json:"command"`
	TaskID  string                `json:"task_id,omitempty"`
	Input   *core.CreateTaskInput `json:"input,omitempty"`
}

type socketEnvelope struct {
	Type        string                 `json:"type"`
	OK          bool                   `json:"ok,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	Task        *core.Task             `json:"task,omitempty"`
	Update      *core.TaskStatusUpdate `json:"update,omitempty"`
}

func NewSocketServer(cfg SocketServerConfig) *SocketServer {
	return &SocketServer{
		socketPath:  cfg.SocketPath,
		daemon:      cfg.Daemon,
		fingerprint: cfg.Fingerprint,
		stop:        cfg.Stop,
	}
}

func (s *SocketServer) Serve(ctx context.Context) error {
	if s.socketPath == "" {
		return fmt.Errorf("status daemon socket path not configured")
	}
	if s.daemon == nil {
		return fmt.Errorf("status daemon not configured")
	}

	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return fmt.Errorf("prepare status daemon socket directory: %w", err)
	}
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale status daemon socket: %w", err)
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on status daemon socket: %w", err)
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
			if ctx.Err() != nil {
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				continue
			}
			return err
		}

		go s.handleConn(ctx, conn)
	}
}

func (s *SocketServer) handleConn(ctx context.Context, conn net.Conn) {
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
		_ = encoder.Encode(socketEnvelope{Type: "health", OK: true, Fingerprint: s.fingerprint})
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

func (s *SocketServer) handleCreateTask(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	if req.Input == nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: "create_task input required"})
		return
	}

	task, err := s.daemon.CreateTask(ctx, *req.Input)
	if err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	_ = encoder.Encode(socketEnvelope{Type: "task_created", OK: true, Task: task})
}

func (s *SocketServer) handleLatestTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	update, err := s.daemon.LatestTaskStatus(ctx, strings.TrimSpace(req.TaskID))
	if err != nil {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: err.Error()})
		return
	}

	_ = encoder.Encode(socketEnvelope{Type: "task_status_snapshot", OK: true, Update: update})
}

func (s *SocketServer) handleSubscribeTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: "subscribe_task_status task_id required"})
		return
	}

	if err := encoder.Encode(socketEnvelope{Type: "subscribed", OK: true}); err != nil {
		return
	}

	updates, err := s.daemon.SubscribeTaskStatus(ctx, taskID)
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

func probeSocketHealth(ctx context.Context, socketPath string) (HealthStatus, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return HealthStatus{}, err
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
		return HealthStatus{}, writeErr
	}
	if readErr != nil {
		return HealthStatus{}, readErr
	}
	if resp.Type != "health" || !resp.OK {
		return HealthStatus{}, fmt.Errorf("status daemon unhealthy")
	}

	return HealthStatus{Fingerprint: resp.Fingerprint}, nil
}

func dialSocketHealth(ctx context.Context, socketPath string) error {
	_, err := probeSocketHealth(ctx, socketPath)
	return err
}

func mustReadAll(r *http.Request) []byte {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	return body
}
