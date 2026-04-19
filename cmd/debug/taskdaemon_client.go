package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"rig/internal/core"
)

type daemonSocketRequest struct {
	Command string                `json:"command"`
	TaskID  string                `json:"task_id,omitempty"`
	Input   *core.CreateTaskInput `json:"input,omitempty"`
}

type daemonSocketEnvelope struct {
	Type   string                 `json:"type"`
	OK     bool                   `json:"ok,omitempty"`
	Error  string                 `json:"error,omitempty"`
	Task   *core.Task             `json:"task,omitempty"`
	Update *core.TaskStatusUpdate `json:"update,omitempty"`
}

func createTaskViaDaemon(ctx context.Context, socketPath string, input core.CreateTaskInput) (*core.Task, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(daemonSocketRequest{
		Command: "create_task",
		Input:   &input,
	}); err != nil {
		return nil, err
	}

	var resp daemonSocketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type != "task_created" || !resp.OK || resp.Task == nil {
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		return nil, fmt.Errorf("unexpected create_task response %q", resp.Type)
	}

	return resp.Task, nil
}

func latestTaskStatusViaDaemon(ctx context.Context, socketPath string, taskID string) (*core.TaskStatusUpdate, error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(daemonSocketRequest{
		Command: "latest_task_status",
		TaskID:  taskID,
	}); err != nil {
		return nil, err
	}

	var resp daemonSocketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Type != "task_status_snapshot" || !resp.OK {
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		return nil, fmt.Errorf("unexpected latest_task_status response %q", resp.Type)
	}

	return resp.Update, nil
}

func subscribeTaskStatusViaDaemon(ctx context.Context, socketPath string, taskID string) (<-chan core.TaskStatusUpdate, func(), error) {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return nil, nil, err
	}

	if err := json.NewEncoder(conn).Encode(daemonSocketRequest{
		Command: "subscribe_task_status",
		TaskID:  taskID,
	}); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}

	decoder := json.NewDecoder(bufio.NewReader(conn))
	var ack daemonSocketEnvelope
	if err := decoder.Decode(&ack); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if ack.Type != "subscribed" || !ack.OK {
		_ = conn.Close()
		if ack.Error != "" {
			return nil, nil, errors.New(ack.Error)
		}
		return nil, nil, fmt.Errorf("unexpected subscribe_task_status response %q", ack.Type)
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
			var msg daemonSocketEnvelope
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

func dialDaemonSocket(ctx context.Context, socketPath string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath)
}
