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

var currentFrontendBuildVersion = "dev"

type socketRequest struct {
	Input   *core.CreateTaskInput `json:"input,omitempty"`
	Command string                `json:"command"`
	TaskID  string                `json:"task_id,omitempty"`
}

type socketEnvelope struct {
	Type    string                 `json:"type"`
	Error   string                 `json:"error,omitempty"`
	Version string                 `json:"version,omitempty"`
	Task    *core.Task             `json:"task,omitempty"`
	Update  *core.TaskStatusUpdate `json:"update,omitempty"`
	Tasks   []*core.Task           `json:"tasks,omitempty"`
	OK      bool                   `json:"ok,omitempty"`
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

func probeFrontendBuildVersion(ctx context.Context, socketPath string) error {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{Command: "frontend_build_version"}); err != nil {
		return err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return err
	}
	if resp.Type == "error" && resp.Error != "" {
		return errors.New(resp.Error)
	}
	if resp.Type != "frontend_build_version" || !resp.OK {
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
