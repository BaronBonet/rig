package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"rig/internal/core"
)

type frontend struct {
	sessions   core.TmuxSessionClient
	socketPath string
}

func (f *frontend) AttachTaskSession(ctx context.Context, task *core.Task) error {
	if f.sessions == nil {
		return fmt.Errorf("task session client not configured")
	}

	return f.sessions.AttachTaskSession(ctx, task)
}

func (f *frontend) GetTaskActivity(ctx context.Context, taskID string, limit int) ([]core.TaskActivityEvent, error) {
	resp, err := f.sendUnary(ctx, socketRequest{
		Command: socketCommandGetTaskActivity,
		TaskID:  taskID,
		Limit:   limit,
	}, socketEnvelopeTaskActivity)
	if err != nil {
		return nil, err
	}

	return resp.Activity, nil
}

func (f *frontend) GetTaskTokenUsage(ctx context.Context, taskID string) (*core.TaskTokenUsage, error) {
	resp, err := f.sendUnary(ctx, socketRequest{
		Command: socketCommandGetTaskTokenUsage,
		TaskID:  taskID,
	}, socketEnvelopeTaskTokenUsage)
	if err != nil {
		return nil, err
	}

	return resp.Usage, nil
}

func (f *frontend) ListRepoPullRequests(ctx context.Context, cwd string) ([]core.RepoPullRequest, error) {
	resp, err := f.sendUnary(ctx, socketRequest{
		Command: socketCommandListRepoPullRequests,
		Cwd:     cwd,
	}, socketEnvelopeRepoPullRequestsList)
	if err != nil {
		return nil, err
	}

	return resp.PullRequests, nil
}

func (f *frontend) PullRequestStatus(
	ctx context.Context,
	repoRoot string,
	branchName string,
) (*core.PRStatus, error) {
	resp, err := f.sendUnary(ctx, socketRequest{
		Command:    socketCommandPullRequestStatus,
		Cwd:        repoRoot,
		BranchName: branchName,
	}, socketEnvelopePullRequestStatus)
	if err != nil {
		return nil, err
	}
	if resp.PR == nil {
		return &core.PRStatus{State: core.PRStateNone}, nil
	}

	return resp.PR, nil
}

func (f *frontend) ReconnectTaskSession(ctx context.Context, taskID string) error {
	_, err := f.sendUnary(ctx, socketRequest{
		Command: socketCommandReconnectTaskSession,
		TaskID:  taskID,
	}, socketEnvelopeTaskSessionReconnect)
	return err
}

func (f *frontend) CreateTaskStream(
	ctx context.Context,
	input core.CreateTaskInput,
) (<-chan core.TaskCreateEvent, error) {
	conn, err := dialDaemonSocket(ctx, f.socketPath)
	if err != nil {
		return nil, err
	}

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: socketCommandCreateTask,
		Input:   &input,
	}); err != nil {
		_ = conn.Close()
		return nil, err
	}

	stopCancelWatch := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})

	events := make(chan core.TaskCreateEvent, 16)
	go func() {
		defer close(events)
		defer conn.Close()
		defer stopCancelWatch()

		decoder := json.NewDecoder(bufio.NewReader(conn))
		for {
			event, recvErr := receiveTaskCreateEvent(decoder)
			if recvErr != nil {
				if errors.Is(recvErr, io.EOF) {
					recvErr = errors.New("create task stream closed before terminal result")
				}
				select {
				case <-ctx.Done():
				case events <- core.TaskCreateEvent{Err: recvErr}:
				}
				return
			}

			select {
			case <-ctx.Done():
				return
			case events <- event:
			}

			if event.Task != nil || event.Err != nil {
				return
			}
		}
	}()

	return events, nil
}

func (f *frontend) DeleteTask(ctx context.Context, taskID string) error {
	_, err := f.sendUnary(ctx, socketRequest{
		Command: socketCommandDeleteTask,
		TaskID:  taskID,
	}, socketEnvelopeTaskDeleted)
	return err
}

func (f *frontend) ListTasks(ctx context.Context) ([]*core.Task, error) {
	resp, err := f.sendUnary(ctx, socketRequest{Command: socketCommandListTasks}, socketEnvelopeTasksList)
	if err != nil {
		return nil, err
	}

	return resp.Tasks, nil
}

func (f *frontend) LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	resp, err := f.sendUnary(ctx, socketRequest{
		Command: socketCommandLatestTaskStatus,
		TaskID:  taskID,
	}, socketEnvelopeTaskStatusSnapshot)
	if err != nil {
		return nil, err
	}

	return resp.Update, nil
}

func (f *frontend) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	conn, err := dialDaemonSocket(ctx, f.socketPath)
	if err != nil {
		return nil, err
	}

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: socketCommandSubscribeTaskStatus,
		TaskID:  taskID,
	}); err != nil {
		_ = conn.Close()
		return nil, err
	}

	stopCancelWatch := context.AfterFunc(ctx, func() {
		_ = conn.Close()
	})
	ownedByReceiveLoop := false
	defer func() {
		if !ownedByReceiveLoop {
			stopCancelWatch()
		}
	}()

	decoder := json.NewDecoder(bufio.NewReader(conn))
	var ack socketEnvelope
	if err := decoder.Decode(&ack); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if ack.Type == socketEnvelopeError && ack.Error != "" {
		_ = conn.Close()
		return nil, errors.New(ack.Error)
	}
	if ack.Type != socketEnvelopeSubscribed || !ack.OK {
		_ = conn.Close()
		return nil, unexpectedResponseError(socketCommandSubscribeTaskStatus, ack)
	}

	updates := make(chan core.TaskStatusUpdate, 16)
	ownedByReceiveLoop = true
	go func() {
		defer close(updates)
		defer conn.Close()
		defer stopCancelWatch()

		for {
			update, recvErr := receiveTaskStatusUpdate(decoder)
			if recvErr != nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case updates <- *update:
			}
		}
	}()

	return updates, nil
}

func (f *frontend) send(ctx context.Context, req socketRequest) (*socketEnvelope, error) {
	conn, err := dialDaemonSocket(ctx, f.socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type == socketEnvelopeError && resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	return &resp, nil
}

func (f *frontend) sendUnary(ctx context.Context, req socketRequest, expectedType string) (*socketEnvelope, error) {
	resp, err := f.send(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Type != expectedType || !resp.OK {
		return nil, unexpectedResponseError(req.Command, *resp)
	}

	return resp, nil
}

func unexpectedResponseError(command string, resp socketEnvelope) error {
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	return fmt.Errorf("unexpected %s response %q", command, resp.Type)
}
