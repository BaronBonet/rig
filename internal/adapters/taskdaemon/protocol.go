package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/BaronBonet/rig/internal/core"
)

var currentFrontendBuildVersion = "dev"

const currentFrontendProtocolVersion = 7

const (
	socketCommandCreateTask           = "create_task"
	socketCommandDeleteTask           = "delete_task"
	socketCommandFrontendBuildVersion = "frontend_build_version"
	socketCommandGetTaskActivity      = "get_task_activity"
	// #nosec G101 -- socket command name, not a credential.
	socketCommandGetTaskTokenUsage     = "get_task_token_usage"
	socketCommandHealth                = "health"
	socketCommandLatestTaskStatus      = "latest_task_status"
	socketCommandListRepoPullRequests  = "list_repo_pull_requests"
	socketCommandListTasks             = "list_tasks"
	socketCommandProtocolVersion       = "protocol_version"
	socketCommandPullRequestStatus     = "pull_request_status"
	socketCommandReconnectTaskSession  = "reconnect_task_session"
	socketCommandRetryTaskCreation     = "retry_task_creation"
	socketCommandStop                  = "stop"
	socketCommandSubscribeTaskStatus   = "subscribe_task_status"
	socketEnvelopeError                = "error"
	socketEnvelopeFrontendBuildVersion = "frontend_build_version"
	socketEnvelopeHealth               = "health"
	socketEnvelopeProtocolVersion      = "protocol_version"
	socketEnvelopeRepoPullRequestsList = "repo_pull_requests_list"
	socketEnvelopeSubscribed           = "subscribed"
	socketEnvelopeStopping             = "stopping"
	socketEnvelopeTaskActivity         = "task_activity"
	socketEnvelopeTaskCreated          = "task_created"
	socketEnvelopeTaskCreateProgress   = "task_create_progress"
	socketEnvelopeTaskDeleted          = "task_deleted"
	socketEnvelopeTaskSessionReconnect = "task_session_reconnected"
	socketEnvelopeTaskStatusSnapshot   = "task_status_snapshot"
	socketEnvelopeTaskStatusUpdate     = "task_status_update"
	socketEnvelopeTaskTokenUsage       = "task_token_usage"
	socketEnvelopeTasksList            = "tasks_list"
	socketEnvelopePullRequestStatus    = "pull_request_status"
)

func SetFrontendBuildVersion(version string) {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "dev"
	}
	currentFrontendBuildVersion = version
}

type socketRequest struct {
	Input      *core.CreateTaskInput `json:"input,omitempty"`
	Command    string                `json:"command"`
	Cwd        string                `json:"cwd,omitempty"`
	BranchName string                `json:"branch_name,omitempty"`
	Limit      int                   `json:"limit,omitempty"`
	TaskID     string                `json:"task_id,omitempty"`
}

type socketEnvelope struct {
	Type            string                        `json:"type"`
	Error           string                        `json:"error,omitempty"`
	Activity        []core.TaskActivityEvent      `json:"activity,omitempty"`
	Version         string                        `json:"version,omitempty"`
	Task            *core.Task                    `json:"task,omitempty"`
	CreateProgress  *core.TaskCreateProgressEvent `json:"create_progress,omitempty"`
	Update          *core.TaskStatusUpdate        `json:"update,omitempty"`
	Usage           *core.TaskTokenUsage          `json:"usage,omitempty"`
	PR              *core.PRStatus                `json:"pr,omitempty"`
	Tasks           []*core.Task                  `json:"tasks,omitempty"`
	PullRequests    []core.RepoPullRequest        `json:"pull_requests,omitempty"`
	ProtocolVersion int                           `json:"protocol_version,omitempty"`
	OK              bool                          `json:"ok,omitempty"`
}

func dialDaemonSocket(ctx context.Context, socketPath string) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}

	return conn, nil
}

func probeSocketHealth(ctx context.Context, socketPath string) error {
	operationCtx, cancel := context.WithTimeout(ctx, socketOperationTimeout)
	defer cancel()

	conn, err := dialDaemonSocket(operationCtx, socketPath)
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
		writeErr = json.NewEncoder(conn).Encode(socketRequest{Command: socketCommandHealth})
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
	if resp.Type != socketEnvelopeHealth || !resp.OK {
		if resp.Error != "" {
			return fmt.Errorf("task daemon unhealthy: %s", resp.Error)
		}
		return fmt.Errorf("task daemon unhealthy")
	}

	return nil
}

func probeFrontendBuildVersion(ctx context.Context, socketPath string) error {
	operationCtx, cancel := context.WithTimeout(ctx, socketOperationTimeout)
	defer cancel()

	conn, err := dialDaemonSocket(operationCtx, socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{Command: socketCommandFrontendBuildVersion}); err != nil {
		return err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return err
	}
	if resp.Type == socketEnvelopeError && resp.Error != "" {
		return errors.New(resp.Error)
	}
	if resp.Type != socketEnvelopeFrontendBuildVersion || !resp.OK {
		return fmt.Errorf("task daemon build version probe failed")
	}
	if resp.Version != currentFrontendBuildVersion {
		return fmt.Errorf(
			"task daemon build version mismatch: got %q want %q",
			resp.Version,
			currentFrontendBuildVersion,
		)
	}

	return nil
}

func probeFrontendProtocol(ctx context.Context, socketPath string) error {
	operationCtx, cancel := context.WithTimeout(ctx, socketOperationTimeout)
	defer cancel()

	conn, err := dialDaemonSocket(operationCtx, socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{Command: socketCommandProtocolVersion}); err != nil {
		return err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return err
	}
	if resp.Type == socketEnvelopeError && resp.Error != "" {
		return errors.New(resp.Error)
	}
	if resp.Type != socketEnvelopeProtocolVersion || !resp.OK {
		return fmt.Errorf("task daemon protocol probe failed")
	}
	if resp.ProtocolVersion != currentFrontendProtocolVersion {
		return fmt.Errorf(
			"task daemon protocol version mismatch: got %d want %d",
			resp.ProtocolVersion,
			currentFrontendProtocolVersion,
		)
	}

	return nil
}

func receiveTaskCreateEvent(decoder *json.Decoder) (core.TaskCreateEvent, error) {
	var msg socketEnvelope
	if err := decoder.Decode(&msg); err != nil {
		if errors.Is(err, io.EOF) {
			return core.TaskCreateEvent{}, err
		}
		return core.TaskCreateEvent{}, err
	}

	switch {
	case msg.Type == socketEnvelopeTaskCreateProgress && msg.CreateProgress != nil:
		return core.TaskCreateEvent{Progress: msg.CreateProgress}, nil
	case msg.Type == socketEnvelopeTaskCreated && msg.Task != nil:
		return core.TaskCreateEvent{Task: msg.Task}, nil
	case msg.Type == socketEnvelopeError && msg.Error != "":
		return core.TaskCreateEvent{Err: errors.New(msg.Error)}, nil
	default:
		return core.TaskCreateEvent{}, fmt.Errorf("unexpected %s response %q", socketCommandCreateTask, msg.Type)
	}
}

func receiveTaskStatusUpdate(decoder *json.Decoder) (*core.TaskStatusUpdate, error) {
	var msg socketEnvelope
	if err := decoder.Decode(&msg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, err
		}
		return nil, err
	}
	if msg.Type == socketEnvelopeError && msg.Error != "" {
		return nil, errors.New(msg.Error)
	}
	if msg.Type != socketEnvelopeTaskStatusUpdate || msg.Update == nil {
		return nil, fmt.Errorf("unexpected %s stream message %q", socketCommandSubscribeTaskStatus, msg.Type)
	}

	return msg.Update, nil
}
