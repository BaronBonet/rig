package tmux

import (
	"bufio"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecControlPipeSendCommand_ReturnsPromptErrorOnErrorTerminator(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()
	defer stdoutW.Close()

	pipe := &execControlPipe{
		stdin:   stdinW,
		stdout:  stdoutR,
		stderr:  io.NopCloser(strings.NewReader("")),
		timeout: 200 * time.Millisecond,
	}
	go pipe.scan()

	errCh := make(chan error, 1)
	go func() {
		_, err := pipe.SendCommand(context.Background(), "list-panes -t =session:agent -F #{pane_id}")
		errCh <- err
	}()

	go func() {
		_, _ = bufio.NewReader(stdinR).ReadString('\n')
		_, _ = stdoutW.Write([]byte("%begin\n"))
		_, _ = stdoutW.Write([]byte("tmux: failed\n"))
		_, _ = stdoutW.Write([]byte("%error\n"))
		_ = stdoutW.Close()
	}()

	select {
	case err := <-errCh:
		require.Error(t, err)
		require.ErrorContains(t, err, "tmux control command failed")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tmux error")
	}
}

func TestExecControlPipeSendCommand_WaitsForInitialAttachHandshake(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()
	defer stdoutW.Close()

	pipe := &execControlPipe{
		stdin:   stdinW,
		stdout:  stdoutR,
		stderr:  io.NopCloser(strings.NewReader("")),
		timeout: time.Second,
		readyCh: make(chan struct{}),
	}
	go pipe.scan()

	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := pipe.SendCommand(
			context.Background(),
			`list-panes -t =session:agent -F "#{pane_id}\t#{pane_current_command}"`,
		)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	go func() {
		_, _ = stdoutW.Write([]byte("%begin 1 1 0\n"))
		_, _ = stdoutW.Write([]byte("%end 1 1 0\n"))

		reader := bufio.NewReader(stdinR)
		_, _ = reader.ReadString('\n')

		_, _ = stdoutW.Write([]byte("%begin 1 2 1\n"))
		_, _ = stdoutW.Write([]byte("%24\tcodex-aarch64-a\n"))
		_, _ = stdoutW.Write([]byte("%31\tzsh\n"))
		_, _ = stdoutW.Write([]byte("%end 1 2 1\n"))
		_ = stdoutW.Close()
	}()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case result := <-resultCh:
		require.Equal(t, "%24\tcodex-aarch64-a\n%31\tzsh", result)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for startup handshake command result")
	}
}

func TestExecControlPipeSendCommand_SerializesOverlappingCommands(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()
	defer stdoutW.Close()

	pipe := &execControlPipe{
		stdin:   stdinW,
		stdout:  stdoutR,
		stderr:  io.NopCloser(strings.NewReader("")),
		timeout: 200 * time.Millisecond,
	}
	go pipe.scan()

	var mu sync.Mutex
	var commands []string
	errCh := make(chan error, 2)
	resultCh := make(chan string, 2)

	go func() {
		reader := bufio.NewReader(stdinR)
		for i := 0; i < 2; i++ {
			command, err := reader.ReadString('\n')
			if err != nil {
				errCh <- err
				return
			}
			mu.Lock()
			commands = append(commands, strings.TrimSpace(command))
			mu.Unlock()

			_, _ = stdoutW.Write([]byte("%begin\n"))
			if strings.TrimSpace(command) == "first" {
				_, _ = stdoutW.Write([]byte("response-first\n"))
			} else {
				_, _ = stdoutW.Write([]byte("response-second\n"))
			}
			_, _ = stdoutW.Write([]byte("%end\n"))
		}
		_ = stdoutW.Close()
	}()

	go func() {
		result, err := pipe.SendCommand(context.Background(), "first")
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()
	go func() {
		result, err := pipe.SendCommand(context.Background(), "second")
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	var results []string
	for len(results) < 2 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case result := <-resultCh:
			results = append(results, result)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for overlapping command results")
		}
	}

	mu.Lock()
	require.ElementsMatch(t, []string{"first", "second"}, commands)
	mu.Unlock()
	require.ElementsMatch(t, []string{"response-first", "response-second"}, results)
}

func TestExecControlPipeSendCommand_RespectsContextCancellation(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()
	defer stdoutR.Close()
	defer stdoutW.Close()

	pipe := &execControlPipe{
		stdin:   stdinW,
		stdout:  stdoutR,
		stderr:  io.NopCloser(strings.NewReader("")),
		timeout: 5 * time.Second,
	}
	go pipe.scan()

	go func() {
		reader := bufio.NewReader(stdinR)
		_, _ = reader.ReadString('\n')
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := pipe.SendCommand(ctx, "list-panes -t =session:agent -F #{pane_id}")

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
}
