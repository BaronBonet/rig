package subprocess

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

const (
	// maxLineSize bounds one stdout line of a line session. A provider's
	// JSON-RPC answers, such as its model list, run to several KB per line.
	maxLineSize = 4 << 20
	// maxStderrSize bounds the stderr a line session keeps for its errors. The
	// latest output is kept.
	maxStderrSize = 64 << 10
)

// lineSessionWaits are how long a line session waits for its process to exit.
type lineSessionWaits struct {
	// stdinEOF is how long Close waits once stdin is closed before it asks the
	// process group to terminate.
	stdinEOF time.Duration
	// terminate is how long Close waits after asking the process group to
	// terminate before it kills the group.
	terminate time.Duration
	// exitStatus is how long Receive waits once stdout ends for the exit
	// status and the rest of stderr.
	exitStatus time.Duration
}

// defaultLineSessionWaits let a process exit on its own once its input ends.
// Claude takes most of a second to exit and rewrites the user's ~/.claude.json
// meanwhile, so a kill mid-exit risks corrupting it.
var defaultLineSessionWaits = lineSessionWaits{
	stdinEOF:   3 * time.Second,
	terminate:  2 * time.Second,
	exitStatus: 5 * time.Second,
}

func (ExecRunner) StartLineSession(ctx context.Context, opts StartLineSessionOptions) (LineSession, error) {
	return startLineSession(ctx, opts, defaultLineSessionWaits)
}

func startLineSession(ctx context.Context, opts StartLineSessionOptions, waits lineSessionWaits) (LineSession, error) {
	cmd := exec.CommandContext(ctx, opts.Name, opts.Args...)
	cmd.Dir = opts.Cwd
	// The process leads its own process group, so shutdown reaches its
	// children too, such as the binary an npm CLI wrapper starts.
	startInOwnProcessGroup(cmd)
	// A done context asks the group to terminate, as Close does; WaitDelay
	// kills the process if it is still running after that.
	cmd.Cancel = func() error { return terminateProcessGroup(cmd.Process) }
	cmd.WaitDelay = pipeCloseDelay

	session := &execLineSession{
		ctx:     ctx,
		opts:    opts,
		waits:   waits,
		cmd:     cmd,
		stderr:  &tailBuffer{limit: maxStderrSize},
		lines:   make(chan string),
		closing: make(chan struct{}),
		exited:  make(chan struct{}),
	}
	cmd.Stderr = session.stderr

	// Stdout is a pipe the session owns rather than cmd.StdoutPipe, which Wait
	// closes as soon as the process exits and so drops lines not yet read.
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, session.commandError(fmt.Errorf("open stdout: %w", err))
	}
	session.stdout = stdoutReader
	cmd.Stdout = stdoutWriter

	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return nil, session.commandError(fmt.Errorf("open stdin: %w", err))
	}
	session.stdin = stdin

	err = cmd.Start()
	// The process holds its own copy of the write end now.
	_ = stdoutWriter.Close()
	if err != nil {
		_ = stdoutReader.Close()
		return nil, session.commandError(err)
	}

	go func() {
		session.waitErr = cmd.Wait()
		close(session.exited)
	}()
	go session.readLines()

	return session, nil
}

type execLineSession struct {
	ctx    context.Context
	opts   StartLineSessionOptions
	waits  lineSessionWaits
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *os.File
	stderr *tailBuffer
	// lines carries stdout lines from readLines, which closes it once stdout
	// ends, after setting readErr.
	lines   chan string
	readErr error
	// closing is closed once Close has stopped the process. It stops
	// readLines when stdout has not ended yet.
	closing chan struct{}
	// exited is closed once Wait returns, after waitErr is set.
	exited    chan struct{}
	waitErr   error
	closeOnce sync.Once
	closeErr  error
}

func (s *execLineSession) Send(line string) error {
	if _, err := io.WriteString(s.stdin, line+"\n"); err != nil {
		return s.commandError(fmt.Errorf("write stdin: %w", err))
	}
	return nil
}

func (s *execLineSession) Receive() (string, error) {
	if s.isClosing() {
		return "", io.EOF
	}

	select {
	case line, ok := <-s.lines:
		if ok {
			return line, nil
		}
		// A done context stops the process, which ends stdout too; report
		// why it stopped rather than that it did.
		if err := s.ctx.Err(); err != nil {
			return "", s.commandError(err)
		}
		return "", s.endOfOutputError()
	case <-s.ctx.Done():
		return "", s.commandError(s.ctx.Err())
	}
}

func (s *execLineSession) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.close()
	})
	return s.closeErr
}

// close lets the process exit on its own once stdin closes, then asks its
// process group to terminate, and kills the group only as a last resort.
func (s *execLineSession) close() error {
	_ = s.stdin.Close()

	signalled := false
	if !s.waitForExit(s.waits.stdinEOF) {
		signalled = true
		_ = terminateProcessGroup(s.cmd.Process)
		if !s.waitForExit(s.waits.terminate) {
			_ = killProcessGroup(s.cmd.Process)
			<-s.exited
		}
	}

	close(s.closing)
	// A process outside the group can still hold stdout open; closing the
	// session's end unblocks readLines.
	_ = s.stdout.Close()

	// A process stopped by Close or by the session's context did what it was
	// told; only a failure of its own is an error.
	stopped := signalled || s.ctx.Err() != nil
	if s.waitErr != nil && !stopped {
		return s.commandError(s.waitErr)
	}
	return nil
}

func (s *execLineSession) waitForExit(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.exited:
		return true
	case <-timer.C:
		return false
	}
}

func (s *execLineSession) isClosing() bool {
	select {
	case <-s.closing:
		return true
	default:
		return false
	}
}

func (s *execLineSession) readLines() {
	defer close(s.lines)

	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineSize)
	for scanner.Scan() {
		select {
		case s.lines <- scanner.Text():
		case <-s.closing:
			return
		}
	}
	s.readErr = scanner.Err()
}

// endOfOutputError reports the end of stdout. A process usually exits as its
// stdout ends, so it waits for the exit status and the rest of stderr and
// reports them when they say more than a clean exit would.
func (s *execLineSession) endOfOutputError() error {
	if errors.Is(s.readErr, os.ErrClosed) {
		return io.EOF
	}
	if s.readErr != nil {
		return s.commandError(fmt.Errorf("read stdout: %w", s.readErr))
	}

	var waitErr error
	if s.waitForExit(s.waits.exitStatus) {
		waitErr = s.waitErr
	}

	switch {
	case waitErr != nil:
		return s.commandError(fmt.Errorf("%w: %w", io.EOF, waitErr))
	case s.stderr.String() != "":
		return s.commandError(io.EOF)
	default:
		return io.EOF
	}
}

func (s *execLineSession) commandError(err error) error {
	return CommandError{
		Cwd:    s.opts.Cwd,
		Name:   s.opts.Name,
		Args:   append([]string(nil), s.opts.Args...),
		Stderr: s.stderr.String(),
		Err:    err,
	}
}

// tailBuffer keeps the last limit bytes written to it. It is safe for
// concurrent use, so a session can read stderr while its process writes it.
type tailBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = append(b.buf, p...)
	if excess := len(b.buf) - b.limit; excess > 0 {
		copy(b.buf, b.buf[excess:])
		b.buf = b.buf[:b.limit]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return string(b.buf)
}
