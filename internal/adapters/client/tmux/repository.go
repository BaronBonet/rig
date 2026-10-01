package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

const (
	promptSubmitDelay      = 500 * time.Millisecond
	promptInputSettleDelay = 500 * time.Millisecond
	taskWindowName         = "task"
)

type repository struct {
	runner subprocess.Runner
	now    func() time.Time
	sleep  func(time.Duration)
	getenv func(string) string
}

func New(runner subprocess.Runner) core.TmuxSessionClient {
	return &repository{
		runner: runner,
		now:    time.Now,
		sleep:  time.Sleep,
		getenv: os.Getenv,
	}
}

func (r *repository) HealthCheck(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", "tmux", "-V")
	return err
}

func (r *repository) StartTaskSession(ctx context.Context, task *core.Task, launch core.TaskSessionLaunchSpec) error {
	// Provider switches launch into an existing idle session; only reconnects
	// and fresh tasks need a new session created.
	alreadyExists := r.sessionExists(ctx, task.TmuxSession)
	if !alreadyExists {
		if err := r.createSession(ctx, task.TmuxSession, task.WorktreePath); err != nil {
			return err
		}
	}

	if len(launch.Command) == 0 {
		return nil
	}

	if err := r.sendKeysToWindow(ctx, task.TmuxSession, taskWindowName, launch.Command); err != nil {
		if alreadyExists {
			return err
		}
		return r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}

	if len(launch.PrefillInput) == 0 {
		return nil
	}

	if err := r.waitForPrompt(ctx, task.TmuxSession, taskWindowName, launch.ReadyMarker); err != nil {
		if alreadyExists {
			return err
		}
		return r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}
	r.sleep(promptInputSettleDelay)

	if err := r.typeInWindow(ctx, task.TmuxSession, taskWindowName, launch.PrefillInput); err != nil {
		if alreadyExists {
			return err
		}
		return r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}

	return nil
}

func (r *repository) sessionExists(ctx context.Context, sessionName string) bool {
	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"has-session",
		"-t",
		exactSessionTarget(sessionName),
	)
	return err == nil
}

func (r *repository) AttachTaskSession(ctx context.Context, task *core.Task) error {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return fmt.Errorf("task tmux session is required")
	}

	command := "attach-session"
	if r.getenv != nil && strings.TrimSpace(r.getenv("TMUX")) != "" {
		command = "switch-client"
	}

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		command,
		"-t",
		exactSessionTarget(task.TmuxSession),
	)
	if isMissingSessionError(err, result) {
		return core.ErrTaskSessionNotFound
	}
	return err
}

func (r *repository) InspectTaskSession(ctx context.Context, task *core.Task) (core.TaskSessionRuntimeState, error) {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return core.TaskSessionRuntimeState{}, nil
	}

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"list-panes",
		"-t",
		exactWindowTarget(task.TmuxSession, taskWindowName),
		"-F",
		"#{pane_current_command}\t#{pane_pid}",
	)
	if isMissingSessionError(err, result) {
		return core.TaskSessionRuntimeState{}, nil
	}
	if err != nil {
		return core.TaskSessionRuntimeState{}, err
	}

	commands, panePIDs := panesFromTmuxOutput(result.Stdout)
	// pane_current_command reports the foreground process title, which some
	// provider CLIs rewrite (Claude Code sets it to its version string), so the
	// comm names of each pane's child processes are reported as well.
	children, childEvidenceAvailable := r.paneChildProcesses(ctx, panePIDs)

	return core.TaskSessionRuntimeState{
		Exists:                          true,
		ActiveCommands:                  append(commands, processCommands(children)...),
		CommandStartedAt:                processCommandStartTimes(children),
		ChildProcessEvidenceUnavailable: !childEvidenceAvailable,
	}, nil
}

func (r *repository) InspectTaskSessions(
	ctx context.Context,
	tasks []*core.Task,
) (map[string]core.TaskSessionRuntimeState, error) {
	states := make(map[string]core.TaskSessionRuntimeState, len(tasks))
	taskIDsBySession := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			continue
		}
		states[taskID] = core.TaskSessionRuntimeState{}
		session := normalizedSessionName(strings.TrimSpace(task.TmuxSession))
		if session != "" {
			taskIDsBySession[session] = append(taskIDsBySession[session], taskID)
		}
	}

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"list-panes",
		"-a",
		"-F",
		"#{session_name}\t#{window_name}\t#{pane_current_command}\t#{pane_pid}",
	)
	if isMissingSessionError(err, result) || isNoTmuxServerError(err, result) {
		return states, nil
	}
	if err != nil {
		return nil, err
	}

	inventory, complete := parsePaneInventory(result.Stdout)
	if !complete {
		return nil, fmt.Errorf("parse tmux pane inventory: incomplete output")
	}
	commandsBySession := make(map[string][]string)
	panePIDsBySession := make(map[string][]string)
	seenSessions := make(map[string]bool)
	allPanePIDsPresent := true
	for _, pane := range inventory {
		if pane.window != taskWindowName {
			continue
		}
		if _, tracked := taskIDsBySession[pane.session]; !tracked {
			continue
		}
		seenSessions[pane.session] = true
		if pane.command != "" {
			commandsBySession[pane.session] = append(commandsBySession[pane.session], pane.command)
		}
		if pane.pid != "" {
			panePIDsBySession[pane.session] = append(panePIDsBySession[pane.session], pane.pid)
		} else {
			allPanePIDsPresent = false
		}
	}

	allPanePIDs := make([]string, 0)
	for _, panePIDs := range panePIDsBySession {
		allPanePIDs = append(allPanePIDs, panePIDs...)
	}
	childProcessesByPID, processInventoryAvailable := r.childProcessesByParentPID(ctx, allPanePIDs)
	childEvidenceAvailable := allPanePIDsPresent && processInventoryAvailable

	for session, taskIDs := range taskIDsBySession {
		commands := commandsBySession[session]
		exists := seenSessions[session]
		if !exists {
			continue
		}

		var children []paneChildProcess
		for _, panePID := range panePIDsBySession[session] {
			children = append(children, childProcessesByPID[panePID]...)
		}
		activeCommands := append(append([]string(nil), commands...), processCommands(children)...)
		for _, taskID := range taskIDs {
			states[taskID] = core.TaskSessionRuntimeState{
				Exists:                          true,
				ActiveCommands:                  append([]string(nil), activeCommands...),
				CommandStartedAt:                processCommandStartTimes(children),
				ChildProcessEvidenceUnavailable: !childEvidenceAvailable,
			}
		}
	}

	return states, nil
}

// paneChildProcess is one direct child of a pane's root process, typically the
// CLI the pane shell is running. startedAt is zero when ps reported no
// parseable elapsed time.
type paneChildProcess struct {
	command   string
	startedAt time.Time
}

// paneChildProcesses returns the direct children of each pane's root process.
func (r *repository) paneChildProcesses(ctx context.Context, panePIDs []string) ([]paneChildProcess, bool) {
	processesByPID, available := r.childProcessesByParentPID(ctx, panePIDs)
	var processes []paneChildProcess
	for _, panePID := range panePIDs {
		processes = append(processes, processesByPID[panePID]...)
	}
	return processes, available
}

func (r *repository) childProcessesByParentPID(
	ctx context.Context,
	panePIDs []string,
) (map[string][]paneChildProcess, bool) {
	processesByPID := make(map[string][]paneChildProcess)
	if len(panePIDs) == 0 {
		return processesByPID, true
	}

	result, err := r.runner.Run(ctx, "", "ps", "-axo", "ppid=,etime=,comm=")
	if err != nil {
		return processesByPID, false
	}

	wanted := make(map[string]bool, len(panePIDs))
	for _, pid := range panePIDs {
		wanted[pid] = true
	}

	now := r.now()
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !wanted[fields[0]] {
			continue
		}
		process := paneChildProcess{command: strings.Join(fields[2:], " ")}
		if elapsed, ok := parseProcessElapsedTime(fields[1]); ok {
			process.startedAt = now.Add(-elapsed)
		}
		processesByPID[fields[0]] = append(processesByPID[fields[0]], process)
	}
	return processesByPID, true
}

// parseProcessElapsedTime parses ps's etime format, [[dd-]hh:]mm:ss.
func parseProcessElapsedTime(raw string) (time.Duration, bool) {
	var days int
	if dayPart, clock, ok := strings.Cut(raw, "-"); ok {
		parsed, err := strconv.Atoi(dayPart)
		if err != nil || parsed < 0 {
			return 0, false
		}
		days, raw = parsed, clock
	}

	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	seconds := 0
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0, false
		}
		seconds = seconds*60 + value
	}
	return time.Duration(days)*24*time.Hour + time.Duration(seconds)*time.Second, true
}

func processCommands(processes []paneChildProcess) []string {
	commands := make([]string, 0, len(processes))
	for _, process := range processes {
		commands = append(commands, process.command)
	}
	return commands
}

// processCommandStartTimes maps each command to when its newest process
// started, or returns nil when no process has a known start time.
func processCommandStartTimes(processes []paneChildProcess) map[string]time.Time {
	var startedAt map[string]time.Time
	for _, process := range processes {
		if process.startedAt.IsZero() {
			continue
		}
		if startedAt == nil {
			startedAt = make(map[string]time.Time)
		}
		if process.startedAt.After(startedAt[process.command]) {
			startedAt[process.command] = process.startedAt
		}
	}
	return startedAt
}

func (r *repository) DeleteTaskSession(ctx context.Context, task *core.Task) error {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return nil
	}

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"kill-session",
		"-t",
		exactSessionTarget(task.TmuxSession),
	)
	if isMissingSessionError(err, result) {
		return nil
	}

	return err
}

func (r *repository) createSession(ctx context.Context, sessionName, workingDir string) error {
	sessionName = normalizedSessionName(sessionName)

	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-session",
		"-d",
		"-s",
		sessionName,
		"-n",
		taskWindowName,
		"-c",
		workingDir,
	)
	if err != nil {
		return err
	}

	r.sleep(promptSubmitDelay)

	_, err = r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-window",
		"-d",
		"-t",
		exactSessionTarget(sessionName),
		"-n",
		"editor",
		"-c",
		workingDir,
	)
	if err == nil {
		return nil
	}

	_, cleanupErr := r.runner.Run(ctx, "", "tmux", "kill-session", "-t", exactSessionTarget(sessionName))
	if cleanupErr != nil {
		return errors.Join(err, cleanupErr)
	}

	return err
}

func (r *repository) cleanupStartedSession(ctx context.Context, sessionName string, cause error) error {
	_, cleanupErr := r.runner.Run(ctx, "", "tmux", "kill-session", "-t", exactSessionTarget(sessionName))
	if cleanupErr != nil {
		return errors.Join(cause, cleanupErr)
	}

	return cause
}

func (r *repository) sendKeysToWindow(ctx context.Context, session, window string, command []string) error {
	quoted := make([]string, 0, len(command))
	for _, part := range command {
		if strings.ContainsRune(part, ' ') {
			quoted = append(quoted, "'"+strings.ReplaceAll(part, "'", "'\\''")+"'")
			continue
		}
		quoted = append(quoted, part)
	}

	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"send-keys",
		"-t",
		exactWindowTarget(session, window),
		strings.Join(quoted, " "),
		"C-m",
	)
	return err
}

func (r *repository) capturePaneContent(ctx context.Context, session, window string) (string, error) {
	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"capture-pane",
		"-t",
		exactWindowTarget(session, window),
		"-p",
	)
	if err != nil {
		return "", err
	}

	return result.Stdout, nil
}

func (r *repository) typeInWindow(ctx context.Context, session, window string, command []string) error {
	text := strings.Join(command, " ")
	bufferName := "rig-prefill-" + normalizedSessionName(session) + "-" + window

	_, err := r.runner.RunWithStdin(ctx, subprocess.RunWithStdinOptions{
		Cwd:   "",
		Name:  "tmux",
		Args:  []string{"load-buffer", "-b", bufferName, "-"},
		Stdin: text,
	})
	if err != nil {
		return fmt.Errorf("load task input into tmux buffer: %w", err)
	}

	_, pasteErr := r.runner.Run(
		ctx,
		"",
		"tmux",
		"paste-buffer",
		"-p",
		"-t",
		exactWindowTarget(session, window),
		"-b",
		bufferName,
	)

	_, deleteErr := r.runner.Run(ctx, "", "tmux", "delete-buffer", "-b", bufferName)
	if pasteErr != nil {
		if deleteErr != nil {
			return errors.Join(pasteErr, deleteErr)
		}
		return pasteErr
	}

	return deleteErr
}

func (r *repository) waitForPrompt(ctx context.Context, session, window, marker string) error {
	const (
		pollInterval = 500 * time.Millisecond
		timeout      = 30 * time.Second
	)

	deadline := r.now().Add(timeout)
	for r.now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		content, err := r.capturePaneContent(ctx, session, window)
		if err == nil && strings.Contains(content, marker) {
			return nil
		}

		r.sleep(pollInterval)
	}

	return errors.New("timed out waiting for " + marker + " prompt")
}

func exactSessionTarget(session string) string {
	return "=" + normalizedSessionName(session)
}

func exactWindowTarget(session, window string) string {
	return "=" + normalizedSessionName(session) + ":" + window
}

func normalizedSessionName(session string) string {
	return strings.ReplaceAll(session, ":", "-")
}

func panesFromTmuxOutput(output string) ([]string, []string) {
	lines := strings.Split(output, "\n")
	commands := make([]string, 0, len(lines))
	pids := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		command, pid, hasPID := strings.Cut(line, "\t")
		command = strings.TrimSpace(command)
		if command != "" {
			commands = append(commands, command)
		}
		if hasPID {
			if pid = strings.TrimSpace(pid); pid != "" {
				pids = append(pids, pid)
			}
		}
	}
	return commands, pids
}

type paneInventoryEntry struct {
	session string
	window  string
	command string
	pid     string
}

func parsePaneInventory(output string) ([]paneInventoryEntry, bool) {
	var inventory []paneInventoryEntry
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, false
		}
		session := normalizedSessionName(strings.TrimSpace(fields[0]))
		window := strings.TrimSpace(fields[1])
		if session == "" || window == "" {
			return nil, false
		}
		inventory = append(inventory, paneInventoryEntry{
			session: session,
			window:  window,
			command: strings.TrimSpace(fields[2]),
			pid:     strings.TrimSpace(fields[3]),
		})
	}
	return inventory, true
}

func isMissingSessionError(err error, result subprocess.Result) bool {
	if err == nil {
		return false
	}

	stderr := result.Stderr
	if strings.TrimSpace(stderr) == "" {
		var commandErr subprocess.CommandError
		if errors.As(err, &commandErr) {
			stderr = commandErr.Stderr
		}
	}

	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "can't find session") ||
		strings.Contains(lower, "can't find window") ||
		strings.Contains(lower, "can't find pane")
}

func isNoTmuxServerError(err error, result subprocess.Result) bool {
	if err == nil {
		return false
	}
	stderr := strings.ToLower(strings.TrimSpace(result.Stderr))
	if stderr == "" {
		var commandErr subprocess.CommandError
		if errors.As(err, &commandErr) {
			stderr = strings.ToLower(strings.TrimSpace(commandErr.Stderr))
		}
	}
	return strings.Contains(stderr, "no server running")
}
