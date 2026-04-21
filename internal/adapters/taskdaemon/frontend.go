package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"rig/internal/core"
)

type frontend struct {
	socketPath string
}

func (f *frontend) CreateTask(ctx context.Context, input core.CreateTaskInput) (*core.Task, error) {
	resp, err := f.send(ctx, socketRequest{
		Command: "create_task",
		Input:   &input,
	})
	if err != nil {
		return nil, err
	}
	if resp.Type != "task_created" || !resp.OK || resp.Task == nil {
		return nil, unexpectedResponseError("create_task", *resp)
	}

	return resp.Task, nil
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
