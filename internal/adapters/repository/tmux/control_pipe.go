package tmux

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type controlPipe interface {
	SendCommand(command string) (string, error)
	LastOutputAt() time.Time
	Close() error
}

type controlPipeFactory interface {
	Attach(session string) (controlPipe, error)
}

type execControlPipeFactory struct{}

func (execControlPipeFactory) Attach(session string) (controlPipe, error) {
	cmd := exec.Command("tmux", "-C", "attach-session", "-t", exactSessionTarget(session))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}

	pipe := &execControlPipe{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		timeout: 5 * time.Second,
	}
	go pipe.scan()
	return pipe, nil
}

type execControlPipe struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	sendMu  sync.Mutex
	mu      sync.RWMutex
	timeout time.Duration

	lastOutputAt    time.Time
	pendingResponse chan controlResponse
	closeOnce       sync.Once
	closeErr        error
}

type controlResponse struct {
	output string
	err    error
}

func (p *execControlPipe) SendCommand(command string) (string, error) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()

	responseCh := make(chan controlResponse, 1)
	p.setPendingResponse(responseCh)

	if _, err := fmt.Fprintln(p.stdin, command); err != nil {
		p.clearPendingResponse(responseCh)
		return "", err
	}

	select {
	case response := <-responseCh:
		return response.output, response.err
	case <-time.After(p.timeout):
		p.clearPendingResponse(responseCh)
		return "", fmt.Errorf("tmux control command timed out: %s", command)
	}
}

func (p *execControlPipe) LastOutputAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastOutputAt
}

func (p *execControlPipe) Close() error {
	p.closeOnce.Do(func() {
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		if p.stdout != nil {
			_ = p.stdout.Close()
		}
		if p.stderr != nil {
			_ = p.stderr.Close()
		}
		if p.cmd != nil && p.cmd.Process != nil {
			p.closeErr = p.cmd.Process.Kill()
		}
		if p.cmd != nil {
			_, _ = p.cmd.Process.Wait()
		}
	})
	return p.closeErr
}

func (p *execControlPipe) scan() {
	scanner := bufio.NewScanner(io.MultiReader(p.stdout, p.stderr))
	var collecting bool
	var responseLines []string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "%output"):
			p.mu.Lock()
			p.lastOutputAt = time.Now().UTC()
			p.mu.Unlock()
		case strings.HasPrefix(line, "%begin"):
			collecting = true
			responseLines = responseLines[:0]
		case strings.HasPrefix(line, "%error"):
			collecting = false
			p.completePending(controlResponse{
				err: fmt.Errorf("tmux control command failed: %s", strings.TrimSpace(strings.Join(responseLines, "\n"))),
			})
			responseLines = responseLines[:0]
		case strings.HasPrefix(line, "%end"):
			collecting = false
			p.completePending(controlResponse{output: strings.Join(responseLines, "\n")})
			responseLines = responseLines[:0]
		default:
			if collecting {
				responseLines = append(responseLines, line)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		p.completePending(controlResponse{err: err})
		return
	}
	p.completePending(controlResponse{err: io.EOF})
}

func (p *execControlPipe) setPendingResponse(responseCh chan controlResponse) {
	p.mu.Lock()
	p.pendingResponse = responseCh
	p.mu.Unlock()
}

func (p *execControlPipe) clearPendingResponse(responseCh chan controlResponse) {
	p.mu.Lock()
	if p.pendingResponse == responseCh {
		p.pendingResponse = nil
	}
	p.mu.Unlock()
}

func (p *execControlPipe) completePending(response controlResponse) {
	p.mu.Lock()
	pending := p.pendingResponse
	p.pendingResponse = nil
	p.mu.Unlock()
	if pending == nil {
		return
	}

	select {
	case pending <- response:
	default:
	}
}
