package tmux

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
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
	// returnKeyEnvVar names the tmux prefix key that returns to rig from a
	// task session; "none" leaves tmux's key bindings alone.
	returnKeyEnvVar  = "RIG_TMUX_RETURN_KEY"
	defaultReturnKey = "b"
	rigSessionOption = "@rig_session"
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
	window := windowOf(task)
	// Provider switches launch into an existing idle session; only reconnects
	// and fresh tasks need a new session created. A child task's window is
	// added to its parent's session, which may already exist.
	alreadyExists := r.sessionExists(ctx, task.TmuxSession)
	session, cwd := task.TmuxSession, task.WorktreePath
	switch {
	case !alreadyExists:
		if err := r.createSession(ctx, session, window, cwd, task.ID, task.ProviderEnv); err != nil {
			return err
		}
	case !r.windowExists(ctx, session, window):
		if err := r.createWindow(ctx, session, window, cwd, task.ID, task.ProviderEnv); err != nil {
			return err
		}
	}

	if len(launch.Command) == 0 {
		return nil
	}

	if err := r.sendKeysToWindow(ctx, task.TmuxSession, window, launch.Command); err != nil {
		if alreadyExists {
			return err
		}
		return r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}

	return nil
}

// windowOf is the window of the task's tmux session its provider runs in.
func windowOf(task *core.Task) string {
	if task == nil {
		return taskWindowName
	}
	if window := strings.TrimSpace(task.TmuxWindow); window != "" {
		return window
	}
	return taskWindowName
}

// PrefillTaskSession types the launch spec's PrefillInput into the task
// window once the provider shows its ready marker, without submitting it. A
// session that fails to take the prompt is left running: the provider is up
// and the prompt is still on the task record.
func (r *repository) PrefillTaskSession(
	ctx context.Context,
	task *core.Task,
	launch core.TaskSessionLaunchSpec,
) error {
	if len(launch.PrefillInput) == 0 {
		return nil
	}

	window := windowOf(task)
	if err := r.waitForPrompt(ctx, task.TmuxSession, window, launch.ReadyMarker, launch.Command); err != nil {
		return err
	}
	r.sleep(promptInputSettleDelay)

	return r.typeInWindow(ctx, task.TmuxSession, window, launch.PrefillInput)
}

// SubmitTaskInput types text literally into the task's window and presses
// Enter after a short settle, so a provider's command suggestion has appeared
// and Enter runs the command rather than only accepting the suggestion.
func (r *repository) SubmitTaskInput(ctx context.Context, task *core.Task, text string) error {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return fmt.Errorf("task tmux session is required")
	}
	target := exactWindowTarget(task.TmuxSession, windowOf(task))
	if _, err := r.runner.Run(ctx, "", "tmux", "send-keys", "-t", target, "-l", text); err != nil {
		return err
	}
	r.sleep(promptInputSettleDelay)
	_, err := r.runner.Run(ctx, "", "tmux", "send-keys", "-t", target, "Enter")
	return err
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

	insideTmux := strings.TrimSpace(r.env("TMUX")) != ""
	command := "attach-session"
	if insideTmux {
		command = "switch-client"
	}
	// A child task lives in one window of its parent's session; attaching
	// lands there.
	target := exactSessionTarget(task.TmuxSession)
	if strings.TrimSpace(task.TmuxWindow) != "" {
		target = exactWindowTarget(task.TmuxSession, task.TmuxWindow)
	}

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		command,
		"-t",
		target,
	)
	if isMissingSessionError(err, result) {
		return core.ErrTaskSessionNotFound
	}
	if err == nil && insideTmux {
		r.rememberReturnToRig(ctx, task.TmuxSession)
	}
	return err
}

// rememberReturnToRig lets one key bring the user back from a task to the
// rig that opened it: the session rig runs in is recorded on the task's
// session, and globally as the fallback for any other session, and the key
// switches to whichever applies. Failures only lose the shortcut.
func (r *repository) rememberReturnToRig(ctx context.Context, taskSession string) {
	key := strings.TrimSpace(r.env(returnKeyEnvVar))
	if key == "" {
		key = defaultReturnKey
	}
	if key == "none" {
		return
	}
	args := []string{"display-message", "-p"}
	if pane := strings.TrimSpace(r.env("TMUX_PANE")); pane != "" {
		args = append(args, "-t", pane)
	}
	result, err := r.runner.Run(ctx, "", "tmux", append(args, "#{session_name}")...)
	rigSession := strings.TrimSpace(result.Stdout)
	if err != nil || rigSession == "" {
		return
	}
	_, _ = r.runner.Run(ctx, "", "tmux", "set-option", "-t", exactSessionTarget(taskSession)+":",
		rigSessionOption, rigSession)
	_, _ = r.runner.Run(ctx, "", "tmux", "set-option", "-g", rigSessionOption, rigSession)
	_, _ = r.runner.Run(ctx, "", "tmux", "bind-key", "-T", "prefix", key,
		"run-shell", "-C", "switch-client -t '=#{"+rigSessionOption+"}'")
}

func (r *repository) env(key string) string {
	if r.getenv == nil {
		return ""
	}
	return r.getenv(key)
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
		exactWindowTarget(task.TmuxSession, windowOf(task)),
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
	// Tasks are keyed by session and window: a child task shares its
	// parent's session and has a window of its own there.
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
			key := session + ":" + windowOf(task)
			taskIDsBySession[key] = append(taskIDsBySession[key], taskID)
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
		key := pane.session + ":" + pane.window
		if _, tracked := taskIDsBySession[key]; !tracked {
			continue
		}
		seenSessions[key] = true
		if pane.command != "" {
			commandsBySession[key] = append(commandsBySession[key], pane.command)
		}
		if pane.pid != "" {
			panePIDsBySession[key] = append(panePIDsBySession[key], pane.pid)
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

	// A child task owns only its window; the session is its parent's.
	args := []string{"kill-session", "-t", exactSessionTarget(task.TmuxSession)}
	if window := strings.TrimSpace(task.TmuxWindow); window != "" {
		args = []string{"kill-window", "-t", exactWindowTarget(task.TmuxSession, window)}
	}
	result, err := r.runner.Run(ctx, "", "tmux", args...)
	if isMissingSessionError(err, result) {
		return nil
	}

	return err
}

func (r *repository) createSession(
	ctx context.Context,
	sessionName, window, workingDir, taskID string,
	providerEnv core.ProviderEnv,
) error {
	sessionName = normalizedSessionName(sessionName)

	args := []string{"new-session", "-d", "-s", sessionName, "-n", window, "-c", workingDir}
	args = append(args, taskEnvArgs(taskID, providerEnv)...)

	_, err := r.runner.Run(ctx, "", "tmux", args...)
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

// taskEnvArgs are the tmux -e flags that give a session or window a task's
// identity and provider configuration. The environment reaches every shell and
// provider started there, and through them the provider hooks that report back
// to Rig; a window's own RIG_TASK_ID is what tells a child task's hooks from
// its parent's. tmux can only set variables, so one the task leaves unset is
// removed by the provider launch command instead.
func taskEnvArgs(taskID string, providerEnv core.ProviderEnv) []string {
	var args []string
	if taskID = strings.TrimSpace(taskID); taskID != "" {
		args = append(args, "-e", core.TaskIDEnvVar+"="+taskID)
	}
	for _, name := range slices.Sorted(maps.Keys(providerEnv)) {
		if value, _ := providerEnv.Lookup(name); value != "" {
			args = append(args, "-e", name+"="+value)
		}
	}
	return args
}

// createWindow adds a task's window to an existing session.
func (r *repository) createWindow(
	ctx context.Context,
	sessionName, window, workingDir, taskID string,
	providerEnv core.ProviderEnv,
) error {
	args := []string{"new-window", "-d", "-t", exactSessionTarget(sessionName), "-n", window, "-c", workingDir}
	args = append(args, taskEnvArgs(taskID, providerEnv)...)
	_, err := r.runner.Run(ctx, "", "tmux", args...)
	if err != nil {
		return err
	}
	r.sleep(promptSubmitDelay)
	return nil
}

// windowExists reports whether the session has a window of that name.
func (r *repository) windowExists(ctx context.Context, sessionName, window string) bool {
	result, err := r.runner.Run(ctx, "", "tmux", "list-windows", "-t", exactSessionTarget(sessionName),
		"-F", "#{window_name}")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == window {
			return true
		}
	}
	return false
}

func (r *repository) cleanupStartedSession(ctx context.Context, sessionName string, cause error) error {
	_, cleanupErr := r.runner.Run(ctx, "", "tmux", "kill-session", "-t", exactSessionTarget(sessionName))
	if cleanupErr != nil {
		return errors.Join(cause, cleanupErr)
	}

	return cause
}

func (r *repository) sendKeysToWindow(ctx context.Context, session, window string, command []string) error {
	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"send-keys",
		"-t",
		exactWindowTarget(session, window),
		strings.Join(quoteShellCommand(command), " "),
		"C-m",
	)
	return err
}

// quoteShellCommand quotes the command words that need it for the shell.
func quoteShellCommand(command []string) []string {
	quoted := make([]string, 0, len(command))
	for _, part := range command {
		if part == "" || strings.ContainsRune(part, ' ') {
			quoted = append(quoted, "'"+strings.ReplaceAll(part, "'", "'\\''")+"'")
			continue
		}
		quoted = append(quoted, part)
	}
	return quoted
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

func (r *repository) waitForPrompt(ctx context.Context, session, window, marker string, command []string) error {
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
		if err == nil && promptReady(content, marker, shellCommandText(command)) {
			return nil
		}

		r.sleep(pollInterval)
	}

	return errors.New("timed out waiting for " + marker + " prompt")
}

// promptReady reports whether the pane shows the provider's ready marker on
// a line of its own. The shell's echo of the launch command is not it: a
// shell prompt may use the same character, and `❯ claude` is on screen before
// the provider has started, so a marker followed by the command, or by the
// start of a command too long for one line, is skipped.
func promptReady(content string, marker string, commandText string) bool {
	if marker == "" {
		return true
	}
	for _, line := range strings.Split(content, "\n") {
		_, rest, found := strings.Cut(line, marker)
		if !found {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" || commandText == "" {
			return true
		}
		if strings.HasPrefix(commandText, rest) || strings.HasPrefix(rest, commandText) {
			continue
		}
		return true
	}
	return false
}

// shellCommandText is the launch command as the shell echoes it.
func shellCommandText(command []string) string {
	return strings.TrimSpace(strings.Join(quoteShellCommand(command), " "))
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
