package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/BaronBonet/rig/internal/core"
)

// frontend is the daemon-backed core.TaskFrontend client. Every TaskService
// method is one callUnary against its descriptor in operations.go; the two
// stream methods and the client-local AttachTaskSession are bespoke.
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
	return callUnary(ctx, f, opGetTaskActivity, taskActivityRequest{TaskID: taskID, Limit: limit})
}

func (f *frontend) GetTaskTokenUsage(ctx context.Context, taskID string) (*core.TaskTokenUsage, error) {
	return callUnary(ctx, f, opGetTaskTokenUsage, taskIDRequest{TaskID: taskID})
}

func (f *frontend) ListRepoPullRequests(ctx context.Context, cwd string) ([]core.RepoPullRequest, error) {
	return callUnary(ctx, f, opListRepoPullRequests, repoPullRequestsRequest{Cwd: cwd})
}

func (f *frontend) PullRequestStatus(
	ctx context.Context,
	repoRoot string,
	branchName string,
) (*core.PRStatus, error) {
	status, err := callUnary(ctx, f, opPullRequestStatus, pullRequestStatusRequest{
		Cwd:        repoRoot,
		BranchName: branchName,
	})
	if err != nil {
		return nil, err
	}
	if status == nil {
		return &core.PRStatus{State: core.PRStateNone}, nil
	}

	return status, nil
}

func (f *frontend) ReconnectTaskSession(ctx context.Context, taskID string) error {
	_, err := callUnary(ctx, f, opReconnectTaskSession, taskIDRequest{TaskID: taskID})
	return err
}

func (f *frontend) GetProviderSetup(ctx context.Context) (*core.ProviderSetup, error) {
	return callUnary(ctx, f, opGetProviderSetup, emptyResponse{})
}

func (f *frontend) SaveProviderSetup(ctx context.Context, setup core.ProviderSetup) error {
	_, err := callUnary(ctx, f, opSaveProviderSetup, saveProviderSetupRequest{ProviderSetup: &setup})
	return err
}

func (f *frontend) DetectProviders(ctx context.Context) ([]core.ProviderDetection, error) {
	return callUnary(ctx, f, opDetectProviders, emptyResponse{})
}

func (f *frontend) SwitchTaskProvider(
	ctx context.Context,
	taskID string,
	provider core.Provider,
) (*core.Task, error) {
	return callUnary(ctx, f, opSwitchTaskProvider, switchTaskProviderRequest{TaskID: taskID, Provider: provider})
}

func (f *frontend) DeleteTask(ctx context.Context, taskID string) error {
	_, err := callUnary(ctx, f, opDeleteTask, taskIDRequest{TaskID: taskID})
	return err
}

func (f *frontend) ListTasks(ctx context.Context) ([]*core.Task, error) {
	return callUnary(ctx, f, opListTasks, emptyResponse{})
}

func (f *frontend) LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	return callUnary(ctx, f, opLatestTaskStatus, taskIDRequest{TaskID: taskID})
}

func (f *frontend) CreateTaskStream(
	ctx context.Context,
	input core.CreateTaskInput,
) (<-chan core.TaskCreateEvent, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", socketCommandCreateTask, err)
	}

	return f.taskCreateEventStream(ctx, socketRequest{
		Command: socketCommandCreateTask,
		Payload: payload,
	})
}

func (f *frontend) RetryTaskCreationStream(
	ctx context.Context,
	taskID string,
) (<-chan core.TaskCreateEvent, error) {
	payload, err := json.Marshal(taskIDRequest{TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", socketCommandRetryTaskCreation, err)
	}

	return f.taskCreateEventStream(ctx, socketRequest{
		Command: socketCommandRetryTaskCreation,
		Payload: payload,
	})
}

func (f *frontend) taskCreateEventStream(
	ctx context.Context,
	request socketRequest,
) (<-chan core.TaskCreateEvent, error) {
	conn, err := dialDaemonSocket(ctx, f.socketPath)
	if err != nil {
		return nil, err
	}

	if err := json.NewEncoder(conn).Encode(request); err != nil {
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

func (f *frontend) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	conn, err := dialDaemonSocket(ctx, f.socketPath)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(taskIDRequest{TaskID: taskID})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%s: encode request: %w", socketCommandSubscribeTaskStatus, err)
	}

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: socketCommandSubscribeTaskStatus,
		Payload: payload,
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
