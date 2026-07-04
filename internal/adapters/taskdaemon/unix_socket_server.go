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
	"syscall"

	"github.com/BaronBonet/rig/internal/core"
)

// unixSocketServer serves core.TaskService over the daemon's Unix socket.
// Unary operations dispatch through the descriptor table in operations.go;
// the handshake probes, the two task-create streams, the status subscription,
// and stop are the only bespoke handlers.
//
// The socket intentionally serves exactly the TaskService port.
// core.TaskFrontend.AttachTaskSession is client-local (it needs the
// foreground terminal), and HandleHookEvent/HealthCheck arrive through their
// own in-process ports, never over this socket.
type unixSocketServer struct {
	service    core.TaskService
	stop       func()
	socketPath string
}

const (
	socketDirMode  = 0o700
	socketFileMode = 0o600
)

func (s *unixSocketServer) Serve(ctx context.Context) error {
	if s.socketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}
	if s.service == nil {
		return fmt.Errorf("task service not configured")
	}

	if err := prepareUnixSocketPath(s.socketPath); err != nil {
		return err
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on task daemon socket: %w", err)
	}
	if err := os.Chmod(s.socketPath, socketFileMode); err != nil {
		_ = listener.Close()
		_ = os.Remove(s.socketPath)
		return fmt.Errorf("secure task daemon socket permissions: %w", err)
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

func prepareUnixSocketPath(socketPath string) error {
	socketDir := filepath.Dir(socketPath)
	if err := os.MkdirAll(socketDir, socketDirMode); err != nil {
		return fmt.Errorf("prepare task daemon socket directory: %w", err)
	}
	if err := os.Chmod(socketDir, socketDirMode); err != nil {
		return fmt.Errorf("secure task daemon socket directory permissions: %w", err)
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale task daemon socket: %w", err)
	}

	return nil
}

func (s *unixSocketServer) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	if err := authorizeUnixSocketPeer(conn); err != nil {
		return
	}

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	decoder := json.NewDecoder(bufio.NewReader(conn))
	encoder := json.NewEncoder(conn)

	var req socketRequest
	if err := decoder.Decode(&req); err != nil {
		_ = writeSocketEnvelope(encoder, errorEnvelope(err))
		return
	}

	if handler, ok := socketUnaryHandlers[req.Command]; ok {
		_ = writeSocketEnvelope(encoder, handler(connCtx, s.service, req.Payload))
		return
	}

	switch req.Command {
	case socketCommandHealth:
		_ = writeHandshakeEnvelope(encoder, handshakeEnvelope{Type: handshakeEnvelopeHealth, OK: true})
	case socketCommandProtocolVersion:
		_ = writeHandshakeEnvelope(encoder, handshakeEnvelope{
			Type:            handshakeEnvelopeProtocolVersion,
			OK:              true,
			ProtocolVersion: currentFrontendProtocolVersion,
		})
	case socketCommandFrontendBuildVersion:
		_ = writeHandshakeEnvelope(encoder, handshakeEnvelope{
			Type:    handshakeEnvelopeFrontendBuildVersion,
			OK:      true,
			Version: currentFrontendBuildVersion,
		})
	case socketCommandCreateTask:
		s.handleCreateTask(ctx, encoder, req)
	case socketCommandRetryTaskCreation:
		s.handleRetryTaskCreation(ctx, encoder, req)
	case socketCommandSubscribeTaskStatus:
		go cancelOnConnClose(conn, cancel)
		s.handleSubscribeTaskStatus(connCtx, encoder, req)
	case socketCommandStop:
		_ = writeHandshakeEnvelope(encoder, handshakeEnvelope{Type: handshakeEnvelopeStopping, OK: true})
		if s.stop != nil {
			s.stop()
		}
	default:
		_ = writeSocketEnvelope(encoder, errorEnvelope(fmt.Errorf("unsupported command %q", req.Command)))
	}
}

func authorizeUnixSocketPeerUID(peerUID uint32, allowedUID uint32) error {
	if peerUID != allowedUID {
		return syscall.EACCES
	}

	return nil
}

func (s *unixSocketServer) handleCreateTask(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	var input core.CreateTaskInput
	if len(req.Payload) == 0 {
		_ = writeSocketEnvelope(encoder, errorEnvelope(errors.New(socketCommandCreateTask+" input required")))
		return
	}
	if err := json.Unmarshal(req.Payload, &input); err != nil {
		decodeErr := fmt.Errorf("%s: decode request: %w", socketCommandCreateTask, err)
		_ = writeSocketEnvelope(encoder, errorEnvelope(decodeErr))
		return
	}

	events, err := s.service.CreateTaskStream(ctx, input)
	if err != nil {
		_ = writeSocketEnvelope(encoder, errorEnvelope(err))
		return
	}

	writeTaskCreateStream(encoder, events)
}

func (s *unixSocketServer) handleRetryTaskCreation(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	var request taskIDRequest
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &request); err != nil {
			decodeErr := fmt.Errorf("%s: decode request: %w", socketCommandRetryTaskCreation, err)
			_ = writeSocketEnvelope(encoder, errorEnvelope(decodeErr))
			return
		}
	}
	taskID := strings.TrimSpace(request.TaskID)
	if taskID == "" {
		_ = writeSocketEnvelope(encoder, errorEnvelope(errors.New(socketCommandRetryTaskCreation+" task ID required")))
		return
	}

	events, err := s.service.RetryTaskCreationStream(ctx, taskID)
	if err != nil {
		_ = writeSocketEnvelope(encoder, errorEnvelope(err))
		return
	}

	writeTaskCreateStream(encoder, events)
}

func writeTaskCreateStream(encoder *json.Encoder, events <-chan core.TaskCreateEvent) {
	for event := range events {
		envelope, err := encodeTaskCreateEvent(event)
		if err != nil {
			_ = writeSocketEnvelope(encoder, errorEnvelope(err))
			return
		}
		if err := writeSocketEnvelope(encoder, envelope); err != nil {
			return
		}
		if event.Task != nil || event.Err != nil {
			return
		}
	}

	_ = writeSocketEnvelope(encoder, errorEnvelope(errors.New("create task stream closed without terminal result")))
}

func (s *unixSocketServer) handleSubscribeTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	var request taskIDRequest
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &request); err != nil {
			decodeErr := fmt.Errorf("%s: decode request: %w", socketCommandSubscribeTaskStatus, err)
			_ = writeSocketEnvelope(encoder, errorEnvelope(decodeErr))
			return
		}
	}
	taskID := strings.TrimSpace(request.TaskID)
	if taskID == "" {
		requiredErr := errors.New(socketCommandSubscribeTaskStatus + " task_id required")
		_ = writeSocketEnvelope(encoder, errorEnvelope(requiredErr))
		return
	}

	updates, err := s.service.SubscribeTaskStatus(ctx, taskID)
	if err != nil {
		_ = writeSocketEnvelope(encoder, errorEnvelope(err))
		return
	}

	if err := writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeSubscribed, OK: true}); err != nil {
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
			envelope, err := encodeTaskStatusUpdate(update)
			if err != nil {
				_ = writeSocketEnvelope(encoder, errorEnvelope(err))
				return
			}
			if err := writeSocketEnvelope(encoder, envelope); err != nil {
				return
			}
		}
	}
}

// writeSocketEnvelope and writeHandshakeEnvelope centralize envelope encoding
// so call sites may ignore write failures on a disconnecting peer without
// tripping errchkjson.
func writeSocketEnvelope(encoder *json.Encoder, envelope socketEnvelope) error {
	return encoder.Encode(envelope)
}

func writeHandshakeEnvelope(encoder *json.Encoder, envelope handshakeEnvelope) error {
	return encoder.Encode(envelope)
}

func cancelOnConnClose(conn net.Conn, cancel context.CancelFunc) {
	var buf [1]byte
	_, _ = conn.Read(buf[:])
	cancel()
}
