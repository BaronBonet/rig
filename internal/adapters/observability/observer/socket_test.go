package observer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestSocketServer_BroadcastsObserverTaskUpdate(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/observer-%d.sock", time.Now().UnixNano())
	hub := NewHub()
	server := NewSocketServer(SocketServerConfig{
		SocketPath: socketPath,
		Hub:        hub,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx)
	}()

	var conn net.Conn
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return false
		default:
		}
		var err error
		conn, err = net.Dial("unix", socketPath)
		return err == nil
	}, 2*time.Second, 20*time.Millisecond)
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	require.NoError(t, encoder.Encode(socketRequest{Command: "subscribe"}))

	decoder := json.NewDecoder(bufio.NewReader(conn))
	var ack socketEnvelope
	require.NoError(t, decoder.Decode(&ack))
	require.Equal(t, "subscribed", ack.Type)

	expected := core.ObserverTaskUpdate{
		TaskID:          "task-1",
		DisplayStatus:   core.DisplayStatusWorking,
		DisplayActivity: core.DisplayActivityCommand,
		LastActivityAt:  time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
	}
	hub.Publish(expected)

	var message socketEnvelope
	require.NoError(t, decoder.Decode(&message))
	require.Equal(t, "task_update", message.Type)
	require.NotNil(t, message.Update)
	require.Equal(t, expected, *message.Update)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)
}
