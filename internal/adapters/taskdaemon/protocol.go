package taskdaemon

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

const currentFrontendProtocolVersion = 1

type socketRequest struct {
	Command string                `json:"command"`
	TaskID  string                `json:"task_id,omitempty"`
	Input   *core.CreateTaskInput `json:"input,omitempty"`
}

type socketEnvelope struct {
	Type    string                 `json:"type"`
	OK      bool                   `json:"ok,omitempty"`
	Error   string                 `json:"error,omitempty"`
	Task    *core.Task             `json:"task,omitempty"`
	Tasks   []*core.Task           `json:"tasks,omitempty"`
	Update  *core.TaskStatusUpdate `json:"update,omitempty"`
	Version int                    `json:"version,omitempty"`
}

func dialDaemonSocket(ctx context.Context, socketPath string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath)
}

func probeSocketHealth(ctx context.Context, socketPath string) error {
	conn, err := dialDaemonSocket(ctx, socketPath)
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
		writeErr = json.NewEncoder(conn).Encode(socketRequest{Command: "health"})
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
	if resp.Type != "health" || !resp.OK {
		if resp.Error != "" {
			return fmt.Errorf("task daemon unhealthy: %s", resp.Error)
		}
		return fmt.Errorf("task daemon unhealthy")
	}

	return nil
}

func probeFrontendProtocol(ctx context.Context, socketPath string) error {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{Command: "protocol_version"}); err != nil {
		return err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return err
	}
	if resp.Type == "error" && resp.Error != "" {
		return errors.New(resp.Error)
	}
	if resp.Type != "protocol_version" || !resp.OK {
		return fmt.Errorf("task daemon protocol probe failed")
	}
	if resp.Version != currentFrontendProtocolVersion {
		return fmt.Errorf("task daemon protocol version mismatch: got %d want %d", resp.Version, currentFrontendProtocolVersion)
	}

	return nil
}

func receiveTaskStatusUpdate(decoder *json.Decoder) (*core.TaskStatusUpdate, error) {
	var msg socketEnvelope
	if err := decoder.Decode(&msg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, err
		}
		return nil, err
	}
	if msg.Type == "error" && msg.Error != "" {
		return nil, errors.New(msg.Error)
	}
	if msg.Type != "task_status_update" || msg.Update == nil {
		return nil, fmt.Errorf("unexpected subscribe_task_status stream message %q", msg.Type)
	}

	return msg.Update, nil
}
