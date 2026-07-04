package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
)

var currentFrontendBuildVersion = "dev"

const currentFrontendProtocolVersion = 9

// socketRequest is one frontend request on the daemon socket. Payload carries
// the operation's typed request body; its shape per command is declared in
// operations.go.
type socketRequest struct {
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// socketEnvelope is one daemon response frame. Payload carries the
// operation's typed response body; its shape per command is declared in
// operations.go.
type socketEnvelope struct {
	Type    string          `json:"type"`
	OK      bool            `json:"ok,omitempty"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

const socketEnvelopeError = "error"

// handshakeEnvelope is the version-negotiation response shape. It must stay
// wire-compatible across every daemon version: the frontend uses these three
// probes to detect a mismatched daemon and restart it, so both ends must be
// able to parse the other's handshake regardless of version. Everything else
// on the wire may change freely with a protocol version bump. See
// docs/adr/0002-version-locked-socket-protocol.md.
type handshakeEnvelope struct {
	Type            string `json:"type"`
	Error           string `json:"error,omitempty"`
	Version         string `json:"version,omitempty"`
	ProtocolVersion int    `json:"protocol_version,omitempty"`
	OK              bool   `json:"ok,omitempty"`
}

const (
	socketCommandHealth               = "health"
	socketCommandProtocolVersion      = "protocol_version"
	socketCommandFrontendBuildVersion = "frontend_build_version"
	socketCommandStop                 = "stop"

	handshakeEnvelopeHealth               = "health"
	handshakeEnvelopeProtocolVersion      = "protocol_version"
	handshakeEnvelopeFrontendBuildVersion = "frontend_build_version"
	handshakeEnvelopeStopping             = "stopping"
)

func SetFrontendBuildVersion(version string) {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "dev"
	}
	currentFrontendBuildVersion = version
}

// FrontendBuildVersion returns the build version embedded in the current frontend binary.
func FrontendBuildVersion() string {
	return currentFrontendBuildVersion
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

func sendHandshakeProbe(ctx context.Context, socketPath string, command string) (handshakeEnvelope, error) {
	operationCtx, cancel := context.WithTimeout(ctx, socketOperationTimeout)
	defer cancel()

	conn, err := dialDaemonSocket(operationCtx, socketPath)
	if err != nil {
		return handshakeEnvelope{}, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{Command: command}); err != nil {
		return handshakeEnvelope{}, err
	}

	var resp handshakeEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return handshakeEnvelope{}, err
	}

	return resp, nil
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
		resp     handshakeEnvelope
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
	if resp.Type != handshakeEnvelopeHealth || !resp.OK {
		if resp.Error != "" {
			return fmt.Errorf("task daemon unhealthy: %s", resp.Error)
		}
		return fmt.Errorf("task daemon unhealthy")
	}

	return nil
}

func probeFrontendBuildVersion(ctx context.Context, socketPath string) error {
	resp, err := sendHandshakeProbe(ctx, socketPath, socketCommandFrontendBuildVersion)
	if err != nil {
		return err
	}
	if resp.Type == socketEnvelopeError && resp.Error != "" {
		return errors.New(resp.Error)
	}
	if resp.Type != handshakeEnvelopeFrontendBuildVersion || !resp.OK {
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
	resp, err := sendHandshakeProbe(ctx, socketPath, socketCommandProtocolVersion)
	if err != nil {
		return err
	}
	if resp.Type == socketEnvelopeError && resp.Error != "" {
		return errors.New(resp.Error)
	}
	if resp.Type != handshakeEnvelopeProtocolVersion || !resp.OK {
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
