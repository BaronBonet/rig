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

// socketBackend is the operation set the daemon socket can actually serve.
// It intentionally excludes core.TaskFrontend.AttachTaskSession: attach needs
// the foreground rig process and its terminal/stdio/TMUX environment, while the
// daemon is a background process serving JSON over a Unix socket.
type socketBackend interface {
	GetTaskActivity(ctx context.Context, taskID string, limit int) ([]core.TaskActivityEvent, error)
	GetTaskTokenUsage(ctx context.Context, taskID string) (*core.TaskTokenUsage, error)
	ListRepoPullRequests(ctx context.Context, cwd string) ([]core.RepoPullRequest, error)
	PullRequestStatus(ctx context.Context, repoRoot string, branchName string) (*core.PRStatus, error)
	ReconnectTaskSession(ctx context.Context, taskID string) error
	CreateTaskStream(ctx context.Context, input core.CreateTaskInput) (<-chan core.TaskCreateEvent, error)
	DeleteTask(ctx context.Context, taskID string) error
	ListTasks(ctx context.Context) ([]*core.Task, error)
	LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error)
	SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error)
}

type unixSocketServer struct {
	backend    socketBackend
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
	if s.backend == nil {
		return fmt.Errorf("task daemon backend not configured")
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
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	switch req.Command {
	case socketCommandHealth:
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeHealth, OK: true})
	case socketCommandProtocolVersion:
		_ = writeSocketEnvelope(encoder, socketEnvelope{
			Type:            socketEnvelopeProtocolVersion,
			OK:              true,
			ProtocolVersion: currentFrontendProtocolVersion,
		})
	case socketCommandFrontendBuildVersion:
		_ = writeSocketEnvelope(encoder, socketEnvelope{
			Type:    socketEnvelopeFrontendBuildVersion,
			OK:      true,
			Version: currentFrontendBuildVersion,
		})
	case socketCommandCreateTask:
		s.handleCreateTask(ctx, encoder, req)
	case socketCommandDeleteTask:
		s.handleDeleteTask(connCtx, encoder, req)
	case socketCommandReconnectTaskSession:
		s.handleReconnectTaskSession(connCtx, encoder, req)
	case socketCommandListRepoPullRequests:
		s.handleListRepoPullRequests(ctx, encoder, req)
	case socketCommandPullRequestStatus:
		s.handlePullRequestStatus(ctx, encoder, req)
	case socketCommandListTasks:
		s.handleListTasks(ctx, encoder)
	case socketCommandGetTaskActivity:
		s.handleGetTaskActivity(connCtx, encoder, req)
	case socketCommandGetTaskTokenUsage:
		s.handleGetTaskTokenUsage(connCtx, encoder, req)
	case socketCommandLatestTaskStatus:
		s.handleLatestTaskStatus(connCtx, encoder, req)
	case socketCommandSubscribeTaskStatus:
		go cancelOnConnClose(conn, cancel)
		s.handleSubscribeTaskStatus(connCtx, encoder, req)
	case socketCommandStop:
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeStopping, OK: true})
		if s.stop != nil {
			s.stop()
		}
	default:
		_ = writeSocketEnvelope(
			encoder,
			socketEnvelope{Type: socketEnvelopeError, Error: fmt.Sprintf("unsupported command %q", req.Command)},
		)
	}
}

func authorizeUnixSocketPeerUID(peerUID uint32, allowedUID uint32) error {
	if peerUID != allowedUID {
		return syscall.EACCES
	}

	return nil
}

func (s *unixSocketServer) handleCreateTask(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	if req.Input == nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{
			Type:  socketEnvelopeError,
			Error: socketCommandCreateTask + " input required",
		})
		return
	}

	events, err := s.backend.CreateTaskStream(ctx, *req.Input)
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	for event := range events {
		switch {
		case event.Progress != nil:
			if err := writeSocketEnvelope(encoder, socketEnvelope{
				Type:           socketEnvelopeTaskCreateProgress,
				OK:             true,
				CreateProgress: event.Progress,
			}); err != nil {
				return
			}
		case event.Task != nil:
			_ = writeSocketEnvelope(
				encoder,
				socketEnvelope{Type: socketEnvelopeTaskCreated, OK: true, Task: event.Task},
			)
			return
		case event.Err != nil:
			_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: event.Err.Error()})
			return
		}
	}

	_ = writeSocketEnvelope(
		encoder,
		socketEnvelope{Type: socketEnvelopeError, Error: "create task stream closed without terminal result"},
	)
}

func (s *unixSocketServer) handleDeleteTask(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = writeSocketEnvelope(encoder, socketEnvelope{
			Type:  socketEnvelopeError,
			Error: socketCommandDeleteTask + " task_id required",
		})
		return
	}

	if err := s.backend.DeleteTask(ctx, taskID); err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeTaskDeleted, OK: true})
}

func (s *unixSocketServer) handleReconnectTaskSession(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = writeSocketEnvelope(
			encoder,
			socketEnvelope{Type: socketEnvelopeError, Error: socketCommandReconnectTaskSession + " task_id required"},
		)
		return
	}

	if err := s.backend.ReconnectTaskSession(ctx, taskID); err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeTaskSessionReconnect, OK: true})
}

func (s *unixSocketServer) handleListTasks(ctx context.Context, encoder *json.Encoder) {
	tasks, err := s.backend.ListTasks(ctx)
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeTasksList, OK: true, Tasks: tasks})
}

func (s *unixSocketServer) handleGetTaskActivity(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = writeSocketEnvelope(encoder, socketEnvelope{
			Type:  socketEnvelopeError,
			Error: socketCommandGetTaskActivity + " task_id required",
		})
		return
	}

	activity, err := s.backend.GetTaskActivity(ctx, taskID, req.Limit)
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeTaskActivity, OK: true, Activity: activity})
}

func (s *unixSocketServer) handleGetTaskTokenUsage(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = writeSocketEnvelope(encoder, socketEnvelope{
			Type:  socketEnvelopeError,
			Error: socketCommandGetTaskTokenUsage + " task_id required",
		})
		return
	}

	usage, err := s.backend.GetTaskTokenUsage(ctx, taskID)
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeTaskTokenUsage, OK: true, Usage: usage})
}

func (s *unixSocketServer) handleListRepoPullRequests(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	prs, err := s.backend.ListRepoPullRequests(ctx, strings.TrimSpace(req.Cwd))
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	_ = writeSocketEnvelope(encoder, socketEnvelope{
		Type:         socketEnvelopeRepoPullRequestsList,
		OK:           true,
		PullRequests: prs,
	})
}

func (s *unixSocketServer) handlePullRequestStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	status, err := s.backend.PullRequestStatus(
		ctx,
		strings.TrimSpace(req.Cwd),
		strings.TrimSpace(req.BranchName),
	)
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	if status == nil {
		status = &core.PRStatus{State: core.PRStateNone}
	}
	_ = writeSocketEnvelope(encoder, socketEnvelope{
		Type: socketEnvelopePullRequestStatus,
		OK:   true,
		PR:   status,
	})
}

func (s *unixSocketServer) handleLatestTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	update, err := s.backend.LatestTaskStatus(ctx, strings.TrimSpace(req.TaskID))
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeTaskStatusSnapshot, OK: true, Update: update})
}

func (s *unixSocketServer) handleSubscribeTaskStatus(ctx context.Context, encoder *json.Encoder, req socketRequest) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		_ = writeSocketEnvelope(encoder, socketEnvelope{
			Type:  socketEnvelopeError,
			Error: socketCommandSubscribeTaskStatus + " task_id required",
		})
		return
	}

	updates, err := s.backend.SubscribeTaskStatus(ctx, taskID)
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	if err := encoder.Encode(socketEnvelope{Type: socketEnvelopeSubscribed, OK: true}); err != nil {
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
			if err := encoder.Encode(socketEnvelope{
				Type:   socketEnvelopeTaskStatusUpdate,
				Update: &update,
			}); err != nil {
				return
			}
		}
	}
}

func writeSocketEnvelope(encoder *json.Encoder, envelope socketEnvelope) error {
	return encoder.Encode(envelope)
}

func cancelOnConnClose(conn net.Conn, cancel context.CancelFunc) {
	var buf [1]byte
	_, _ = conn.Read(buf[:])
	cancel()
}
