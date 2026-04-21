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

func (f *frontend) OpenTaskSession(ctx context.Context, task *core.Task) error {
	if f.sessions == nil {
		return fmt.Errorf("task session client not configured")
	}

	return f.sessions.OpenTaskSession(ctx, task)
}

func (f *frontend) CreateTask(ctx context.Context, input core.CreateTaskInput) (*core.Task, error) {
	events, err := f.CreateTaskStream(ctx, input)
	if err != nil {
		return nil, err
	}

	for event := range events {
		if event.Err != nil {
			return nil, event.Err
		}
		if event.Task != nil {
			return event.Task, nil
		}
	}

	return nil, fmt.Errorf("create task stream closed without terminal result")
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
		Command: "create_task",
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
				if !errors.Is(recvErr, io.EOF) {
					select {
					case <-ctx.Done():
					case events <- core.TaskCreateEvent{Err: recvErr}:
					}
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
	resp, err := f.send(ctx, socketRequest{
		Command: "delete_task",
		TaskID:  taskID,
	})
	if err != nil {
		return err
	}
	if resp.Type != "task_deleted" || !resp.OK {
		return unexpectedResponseError("delete_task", *resp)
	}

	return nil
}

func (f *frontend) ListTasks(ctx context.Context) ([]*core.Task, error) {
	resp, err := f.send(ctx, socketRequest{Command: "list_tasks"})
	if err != nil {
		return nil, err
	}
	if resp.Type != "tasks_list" || !resp.OK {
		return nil, unexpectedResponseError("list_tasks", *resp)
	}

	return resp.Tasks, nil
}

func (f *frontend) LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	resp, err := f.send(ctx, socketRequest{
		Command: "latest_task_status",
		TaskID:  taskID,
	})
	if err != nil {
		return nil, err
	}
	if resp.Type != "task_status_snapshot" || !resp.OK {
		return nil, unexpectedResponseError("latest_task_status", *resp)
	}

	return resp.Update, nil
}

func (f *frontend) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	conn, err := dialDaemonSocket(ctx, f.socketPath)
	if err != nil {
		return nil, err
	}

	if err := json.NewEncoder(conn).Encode(socketRequest{
		Command: "subscribe_task_status",
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
	if ack.Type == "error" && ack.Error != "" {
		_ = conn.Close()
		return nil, errors.New(ack.Error)
	}
	if ack.Type != "subscribed" || !ack.OK {
		_ = conn.Close()
		return nil, unexpectedResponseError("subscribe_task_status", ack)
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
	if resp.Type == "error" && resp.Error != "" {
		return nil, errors.New(resp.Error)
	}

	return &resp, nil
}

func unexpectedResponseError(command string, resp socketEnvelope) error {
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	return fmt.Errorf("unexpected %s response %q", command, resp.Type)
}
