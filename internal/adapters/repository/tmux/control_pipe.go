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
		done:    make(chan struct{}),
		errCh:   make(chan error, 1),
		waitCh:  make(chan struct{}),
		closed:  make(chan struct{}),
		timeout: 5 * time.Second,
	}
	go pipe.scan()
	return pipe, nil
}

type execControlPipe struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu              sync.RWMutex
	lastOutputAt    time.Time
	pendingResponse chan string

	done    chan struct{}
	errCh   chan error
	waitCh  chan struct{}
	closed  chan struct{}
	timeout time.Duration
	closeOnce sync.Once
}

func (p *execControlPipe) SendCommand(command string) (string, error) {
	p.mu.Lock()
	responseCh := make(chan string, 1)
	p.pendingResponse = responseCh
	p.mu.Unlock()

	if _, err := fmt.Fprintln(p.stdin, command); err != nil {
		p.mu.Lock()
		p.pendingResponse = nil
		p.mu.Unlock()
		return "", err
	}

	select {
	case output := <-responseCh:
		return output, nil
	case err := <-p.errCh:
		return "", err
	case <-time.After(p.timeout):
		return "", fmt.Errorf("tmux control command timed out: %s", command)
	}
}

func (p *execControlPipe) LastOutputAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastOutputAt
}

func (p *execControlPipe) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.closed)
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
			err = p.cmd.Process.Kill()
		}
		if p.cmd != nil {
			_, _ = p.cmd.Process.Wait()
		}
	})
	return err
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
		case strings.HasPrefix(line, "%end"):
			collecting = false
			p.mu.Lock()
			pending := p.pendingResponse
			p.pendingResponse = nil
			p.mu.Unlock()
			if pending != nil {
				pending <- strings.Join(responseLines, "\n")
			}
			responseLines = responseLines[:0]
		default:
			if collecting {
				responseLines = append(responseLines, line)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case p.errCh <- err:
		default:
		}
	}
	close(p.done)
}
