package statusstream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"rig/internal/core"
)

type SocketServerConfig struct {
	SocketPath  string
	Hub         *Hub
	Fingerprint string
	Stop        func()
}

type SocketServer struct {
	socketPath  string
	hub         *Hub
	fingerprint string
	stop        func()
}

type HealthStatus struct {
	Fingerprint string
}

type socketRequest struct {
	Command string `json:"command"`
}

type socketEnvelope struct {
	Type        string                 `json:"type"`
	OK          bool                   `json:"ok,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Fingerprint string                 `json:"fingerprint,omitempty"`
	Update      *core.TaskStatusUpdate `json:"update,omitempty"`
}

func NewSocketServer(cfg SocketServerConfig) *SocketServer {
	return &SocketServer{
		socketPath:  cfg.SocketPath,
		hub:         cfg.Hub,
		fingerprint: cfg.Fingerprint,
		stop:        cfg.Stop,
	}
}

func (s *SocketServer) Serve(ctx context.Context) error {
	if s == nil || s.socketPath == "" {
		return fmt.Errorf("status observer socket path not configured")
	}

	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return fmt.Errorf("prepare status observer socket directory: %w", err)
	}
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale status observer socket: %w", err)
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on status observer socket: %w", err)
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
	case "subscribe":
		s.serveSubscription(ctx, encoder)
	case "stop":
		_ = encoder.Encode(socketEnvelope{Type: "stopping", OK: true})
		if s.stop != nil {
			s.stop()
		}
	default:
		_ = encoder.Encode(socketEnvelope{Type: "error", Error: fmt.Sprintf("unsupported command %q", req.Command)})
	}
}

func (s *SocketServer) serveSubscription(ctx context.Context, encoder *json.Encoder) {
	if err := encoder.Encode(socketEnvelope{Type: "subscribed", OK: true}); err != nil {
		return
	}

	updates, release := s.hub.Subscribe(ctx)
	defer release()

	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if err := encoder.Encode(socketEnvelope{
				Type:   "task_status_update",
				Update: &update,
			}); err != nil {
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
		return HealthStatus{}, fmt.Errorf("status observer unhealthy")
	}

	return HealthStatus{Fingerprint: resp.Fingerprint}, nil
}

func dialSocketHealth(ctx context.Context, socketPath string) error {
	_, err := probeSocketHealth(ctx, socketPath)
	return err
}

func Subscribe(ctx context.Context, socketPath string) (<-chan core.TaskStatusUpdate, func(), error) {
	if socketPath == "" {
		return nil, func() {}, fmt.Errorf("status observer socket path not configured")
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, func() {}, err
	}

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(socketRequest{Command: "subscribe"}); err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}

	decoder := json.NewDecoder(bufio.NewReader(conn))
	var ack socketEnvelope
	if err := decoder.Decode(&ack); err != nil {
		_ = conn.Close()
		return nil, func() {}, err
	}
	if ack.Type != "subscribed" {
		_ = conn.Close()
		if ack.Error != "" {
			return nil, func() {}, errors.New(ack.Error)
		}
		return nil, func() {}, fmt.Errorf("unexpected status observer subscribe response %q", ack.Type)
	}

	updates := make(chan core.TaskStatusUpdate, 16)
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = conn.Close()
		})
	}

	go func() {
		defer close(updates)
		defer cleanup()

		for {
			var msg socketEnvelope
			if err := decoder.Decode(&msg); err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				return
			}
			if msg.Type != "task_status_update" || msg.Update == nil {
				continue
			}

			select {
			case <-ctx.Done():
				return
			case updates <- *msg.Update:
			}
		}
	}()

	return updates, cleanup, nil
}
