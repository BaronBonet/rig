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

// StartTaskSession creates the task's session and launches the agent in its
// task window. It never launches into a session that already exists, where an
// agent may be running: typed keys would reach that agent.
func (r *repository) StartTaskSession(
	ctx context.Context,
	task *core.Task,
	launch core.TaskSessionLaunchSpec,
) (core.TmuxPaneRef, error) {
	if r.sessionExists(ctx, task.TmuxSession) {
		return core.TmuxPaneRef{}, fmt.Errorf(
			"%w: %s",
			core.ErrTaskSessionExists,
			normalizedSessionName(task.TmuxSession),
		)
	}
	pane, err := r.createSession(ctx, task.TmuxSession, task.WorktreePath)
	if err != nil {
		return core.TmuxPaneRef{}, err
	}

	if len(launch.Command) == 0 {
		return pane, nil
	}

	if err := r.sendKeys(ctx, exactWindowTarget(task.TmuxSession, taskWindowName), launch.Command); err != nil {
		return core.TmuxPaneRef{}, r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}

	if len(launch.PrefillInput) == 0 {
		return pane, nil
	}

	if err := r.waitForPrompt(ctx, task.TmuxSession, taskWindowName, launch.ReadyMarker); err != nil {
		return core.TmuxPaneRef{}, r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}
	r.sleep(promptInputSettleDelay)

	if err := r.typeInWindow(ctx, task.TmuxSession, taskWindowName, launch.PrefillInput); err != nil {
		return core.TmuxPaneRef{}, r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}

	return pane, nil
}

// SplitAgentPane opens a pane at the right edge of the window that holds pane
// beside, or of the task window when beside is empty, spanning the window's
// height and without selecting it. It evens out the window's panes side by
// side and launches the agent in the new pane. Splitting at the right edge
// puts agents split in one after another left to right in that order; the
// window is found through a pane ID because windows can be renamed. When
// beside has closed, the task window is split instead.
func (r *repository) SplitAgentPane(
	ctx context.Context,
	task *core.Task,
	beside string,
	launch core.TaskSessionLaunchSpec,
) (core.TmuxPaneRef, error) {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return core.TmuxPaneRef{}, fmt.Errorf("task tmux session is required")
	}
	if len(launch.Command) == 0 {
		return core.TmuxPaneRef{}, fmt.Errorf("agent launch command is required")
	}
	taskWindow := exactWindowTarget(task.TmuxSession, taskWindowName)
	window := strings.TrimSpace(beside)
	if window == "" {
		window = taskWindow
	}

	result, err := r.splitWindowEdge(ctx, task, window)
	if window != taskWindow && isMissingPaneError(err, result) {
		// The right edge is the same whichever pane of the window is split,
		// so the task window takes the agent where beside would have.
		window = taskWindow
		result, err = r.splitWindowEdge(ctx, task, window)
	}
	if isMissingWindowError(err, result) {
		return core.TmuxPaneRef{}, fmt.Errorf("task window %q is gone: %w", taskWindowName, err)
	}
	if isMissingSessionError(err, result) {
		return core.TmuxPaneRef{}, core.ErrTaskSessionNotFound
	}
	if err != nil {
		return core.TmuxPaneRef{}, err
	}
	pane, ok := parsePaneRef(result.Stdout)
	if !ok {
		err := fmt.Errorf("tmux reported no usable pane for the new agent pane: %q", result.Stdout)
		if pane.ID == "" {
			return core.TmuxPaneRef{}, err
		}
		return core.TmuxPaneRef{}, r.closeAgentPane(ctx, pane.ID, window, err)
	}
	r.evenOutAgentPanes(ctx, pane.ID)

	if err := r.sendKeys(ctx, pane.ID, launch.Command); err != nil {
		return core.TmuxPaneRef{}, r.closeAgentPane(ctx, pane.ID, window, err)
	}
	return pane, nil
}

// splitWindowEdge opens a detached pane at the right edge of the window that
// holds target, spanning its height, and reports the new pane.
func (r *repository) splitWindowEdge(ctx context.Context, task *core.Task, target string) (subprocess.Result, error) {
	return r.runner.Run(
		ctx,
		"",
		"tmux",
		"split-window",
		"-d",
		"-h",
		"-f",
		"-P",
		"-F",
		paneRefFormat,
		"-t",
		target,
		"-c",
		task.WorktreePath,
	)
}

// evenOutAgentPanes gives every pane of the window that holds target an equal
// share of its width. It is best-effort: uneven panes still run their agents.
func (r *repository) evenOutAgentPanes(ctx context.Context, target string) {
	_, _ = r.runner.Run(ctx, "", "tmux", "select-layout", "-t", target, "even-horizontal")
}

// closeAgentPane closes a pane SplitAgentPane opened but could not launch in,
// so no stray shell is left, and evens out the panes left in its window. It
// runs even when the caller's context is done.
func (r *repository) closeAgentPane(ctx context.Context, paneID string, window string, cause error) error {
	ctx = context.WithoutCancel(ctx)
	_, closeErr := r.runner.Run(ctx, "", "tmux", "kill-pane", "-t", paneID)
	if closeErr == nil {
		r.evenOutAgentPanes(ctx, window)
	}
	return errors.Join(cause, closeErr)
}

func (r *repository) sessionExists(ctx context.Context, sessionName string) bool {
	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"has-session",
		"-t",
		exactSessionOnlyTarget(sessionName),
	)
	return err == nil
}

// AttachTaskSession attaches to the task's Session or, when pane is set, to
// that pane. A pane that no longer exists falls back to the task's Session,
// and so does a pane ID that this client's tmux server, such as another
// server the client runs inside, gives to a different pane.
func (r *repository) AttachTaskSession(ctx context.Context, task *core.Task, pane *core.TmuxPaneRef) error {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return fmt.Errorf("task tmux session is required")
	}

	insideTmux := r.getenv != nil && strings.TrimSpace(r.getenv("TMUX")) != ""
	if pane != nil && strings.TrimSpace(pane.ID) != "" {
		if attached, err := r.attachPane(ctx, *pane, insideTmux); attached || err != nil {
			return err
		}
	}

	command := "attach-session"
	if insideTmux {
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

// attachPane lands the client on an agent's pane. Pane IDs restart on every
// tmux server, so it first checks that the client's server is the pane's;
// display-message reports the server even for a pane that no longer exists,
// which the attach then finds. Inside tmux the client switches to the pane,
// which selects its session, window and pane at once. Outside, the pane's
// window and the pane are selected before attaching to the session that
// holds it: attach-session selects the window of a pane target but not the
// pane. attached is false, with no error, when the pane cannot be targeted.
func (r *repository) attachPane(ctx context.Context, pane core.TmuxPaneRef, insideTmux bool) (bool, error) {
	paneID := strings.TrimSpace(pane.ID)
	// When tmux cannot say, the task's Session attach reports why.
	if server, ok := r.clientTmuxServer(ctx, paneID); !ok || server != pane.Server {
		return false, nil
	}

	args := []string{"switch-client", "-t", paneID}
	if !insideTmux {
		// tmux stops a command sequence at its first failing command.
		args = []string{"select-window", "-t", paneID, ";", "select-pane", "-t", paneID, ";",
			"attach-session", "-t", paneID}
	}
	result, err := r.runner.Run(ctx, "", "tmux", args...)
	if isMissingPaneError(err, result) {
		return false, nil
	}
	return err == nil, err
}

// clientTmuxServer returns the tmux server this client's tmux commands reach,
// as display-message reports it for target, or false when tmux cannot say.
func (r *repository) clientTmuxServer(ctx context.Context, target string) (core.TmuxServer, bool) {
	result, err := r.runner.Run(ctx, "", "tmux", "display-message", "-p", "-t", target, "#{socket_path}\t#{pid}")
	if err != nil {
		return core.TmuxServer{}, false
	}
	socketPath, serverPID, _ := strings.Cut(strings.TrimRight(result.Stdout, "\r\n"), "\t")
	return tmuxServer(socketPath, serverPID), true
}

// InspectTaskSession reports whether the task's session exists, whichever of
// its windows are still open. Without a tmux server it does not.
func (r *repository) InspectTaskSession(ctx context.Context, task *core.Task) (core.TaskSessionRuntimeState, error) {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return core.TaskSessionRuntimeState{}, nil
	}

	result, err := r.runner.Run(ctx, "", "tmux", "has-session", "-t", exactSessionOnlyTarget(task.TmuxSession))
	if isMissingSessionError(err, result) || isNoTmuxServerError(err, result) {
		return core.TaskSessionRuntimeState{}, nil
	}
	if err != nil {
		return core.TaskSessionRuntimeState{}, err
	}

	return core.TaskSessionRuntimeState{Exists: true}, nil
}

// paneInventoryFormat lists every pane on the server. socket_path and pid are
// server-wide, so every line repeats the server's identity.
const paneInventoryFormat = "#{socket_path}\t#{pid}\t#{session_name}\t#{window_index}\t#{window_name}\t" +
	"#{pane_index}\t#{pane_id}\t#{pane_current_command}\t#{pane_pid}"

func (r *repository) InspectTaskSessions(ctx context.Context, tasks []*core.Task) (core.TmuxSnapshot, error) {
	snapshot, _, err := r.inspectServer(ctx, tasks)
	return snapshot, err
}

func (r *repository) LocateProcessPane(
	ctx context.Context,
	task *core.Task,
	pid int,
) (core.TmuxSnapshot, core.ProcessPane, error) {
	snapshot, processes, err := r.inspectServer(ctx, []*core.Task{task})
	if err != nil {
		return core.TmuxSnapshot{}, core.ProcessPane{}, err
	}
	if pid <= 0 || len(snapshot.Panes) == 0 {
		return snapshot, core.ProcessPane{}, nil
	}
	if !processes.available {
		return core.TmuxSnapshot{}, core.ProcessPane{}, fmt.Errorf(
			"locate pane of process %d: process inventory unavailable",
			pid,
		)
	}
	return snapshot, processPane(pid, processes.entries, snapshot.Panes), nil
}

// inspectServer takes one snapshot of the tmux server for the supplied tasks,
// along with the process inventory it was built from.
func (r *repository) inspectServer(
	ctx context.Context,
	tasks []*core.Task,
) (core.TmuxSnapshot, processInventory, error) {
	var snapshot core.TmuxSnapshot
	taskIDsBySession := make(map[string][]string, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		taskID := strings.TrimSpace(task.ID)
		if taskID == "" {
			continue
		}
		session := normalizedSessionName(strings.TrimSpace(task.TmuxSession))
		if session != "" {
			taskIDsBySession[session] = append(taskIDsBySession[session], taskID)
		}
	}

	result, err := r.runner.Run(ctx, "", "tmux", "list-panes", "-a", "-F", paneInventoryFormat)
	if isMissingSessionError(err, result) || isNoTmuxServerError(err, result) {
		return snapshot, processInventory{available: true}, nil
	}
	if err != nil {
		return core.TmuxSnapshot{}, processInventory{}, err
	}

	inventory, complete := parsePaneInventory(result.Stdout)
	if !complete {
		return core.TmuxSnapshot{}, processInventory{}, fmt.Errorf("parse tmux pane inventory: incomplete output")
	}
	allPanePIDs := make([]string, 0, len(inventory))
	for _, pane := range inventory {
		if pane.pid != "" {
			allPanePIDs = append(allPanePIDs, pane.pid)
		}
	}
	processes := r.listProcessesForPanes(ctx, allPanePIDs)
	childProcessesByPID := childProcessesByParentPID(processes.entries, allPanePIDs)

	snapshot.TaskSessionPanes = taskSessionPanes(inventory, taskIDsBySession)
	snapshot.Server, snapshot.Panes = inventoryPanes(
		inventory,
		taskIDsBySession,
		childProcessesByPID,
		processes.available,
	)
	return snapshot, processes, nil
}

// taskSessionPanes lists the IDs of every pane in each tracked task's Session,
// across all its windows, keyed by task ID.
func taskSessionPanes(inventory []paneInventoryEntry, taskIDsBySession map[string][]string) map[string][]string {
	panesByTask := make(map[string][]string)
	listed := make(map[string]bool)
	for _, entry := range inventory {
		for _, taskID := range taskIDsBySession[entry.session] {
			key := taskID + "\x00" + entry.id
			if listed[key] {
				continue
			}
			listed[key] = true
			panesByTask[taskID] = append(panesByTask[taskID], entry.id)
		}
	}
	return panesByTask
}

// maxProcessAncestry bounds the walk from a process up to its pane, which also
// ends a cycle in a malformed process listing. Real chains are a few
// processes deep: hook forwarder, agent CLI, pane shell.
const maxProcessAncestry = 64

// processPane walks from pid up through its ancestors to the first pane root
// process, collecting each process's command on the way. It finds no pane
// when the walk leaves the process inventory or passes maxProcessAncestry.
func processPane(pid int, processes []processEntry, panes []core.TmuxPane) core.ProcessPane {
	paneByRootPID := make(map[int]string, len(panes))
	for _, pane := range panes {
		if pane.PID > 0 {
			paneByRootPID[pane.PID] = pane.ID
		}
	}
	processByPID := make(map[int]processEntry, len(processes))
	for _, process := range processes {
		processByPID[process.pid] = process
	}

	var chain []string
	for depth := 0; depth < maxProcessAncestry && pid > 0; depth++ {
		process, listed := processByPID[pid]
		if listed {
			chain = append(chain, process.process.Command)
		}
		if paneID, ok := paneByRootPID[pid]; ok {
			return core.ProcessPane{Pane: paneID, Chain: chain}
		}
		if !listed {
			return core.ProcessPane{}
		}
		pid = process.ppid
	}
	return core.ProcessPane{}
}

// inventoryPanes returns the server's identity and each pane once. list-panes
// -a prints a pane once per session and window it is linked into, so grouped
// sessions and linked windows repeat pane IDs; a repeat replaces the kept
// entry only when it puts the pane in a task Session and the kept entry does
// not.
func inventoryPanes(
	inventory []paneInventoryEntry,
	taskIDsBySession map[string][]string,
	childProcessesByPID map[string][]core.PaneProcess,
	processInventoryAvailable bool,
) (core.TmuxServer, []core.TmuxPane) {
	var server core.TmuxServer
	panes := make([]core.TmuxPane, 0, len(inventory))
	positionByID := make(map[string]int, len(inventory))
	for _, entry := range inventory {
		if server.IsZero() {
			server = entry.server
		}
		panePID, _ := strconv.Atoi(entry.pid)
		pane := core.TmuxPane{
			ID:                              entry.id,
			Session:                         entry.session,
			WindowIndex:                     entry.windowIndex,
			WindowName:                      entry.window,
			PaneIndex:                       entry.paneIndex,
			Command:                         entry.command,
			PID:                             panePID,
			Children:                        childProcessesByPID[entry.pid],
			ChildProcessEvidenceUnavailable: entry.pid == "" || !processInventoryAvailable,
		}
		position, seen := positionByID[entry.id]
		if !seen {
			positionByID[entry.id] = len(panes)
			panes = append(panes, pane)
			continue
		}
		_, keptInTaskSession := taskIDsBySession[panes[position].Session]
		_, inTaskSession := taskIDsBySession[entry.session]
		if inTaskSession && !keptInTaskSession {
			panes[position] = pane
		}
	}
	return server, panes
}

// processInventoryColumns lists every process with its PID, parent PID,
// elapsed time and command name.
const processInventoryColumns = "pid=,ppid=,etime=,comm="

// processInventory is one ps listing of every process. available is false
// when ps failed, so the listing proves nothing.
type processInventory struct {
	entries   []processEntry
	available bool
}

// processEntry is one process of the inventory. parentPID is the parent PID
// as ps printed it, which matches tmux's pane_pid text.
type processEntry struct {
	process   core.PaneProcess
	parentPID string
	pid       int
	ppid      int
}

// listProcessesForPanes lists every process once. Without panes nothing needs
// process evidence, so ps does not run.
func (r *repository) listProcessesForPanes(ctx context.Context, panePIDs []string) processInventory {
	if len(panePIDs) == 0 {
		return processInventory{available: true}
	}

	result, err := r.runner.Run(ctx, "", "ps", "-axo", processInventoryColumns)
	if err != nil {
		return processInventory{}
	}

	now := r.now()
	inventory := processInventory{available: true}
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr != nil || ppidErr != nil {
			continue
		}
		entry := processEntry{
			process:   core.PaneProcess{Command: strings.Join(fields[3:], " ")},
			parentPID: fields[1],
			pid:       pid,
			ppid:      ppid,
		}
		if elapsed, ok := parseProcessElapsedTime(fields[2]); ok {
			entry.process.StartedAt = now.Add(-elapsed)
		}
		inventory.entries = append(inventory.entries, entry)
	}
	return inventory
}

// childProcessesByParentPID groups the direct children of each pane's root
// process by the pane PID.
func childProcessesByParentPID(processes []processEntry, panePIDs []string) map[string][]core.PaneProcess {
	wanted := make(map[string]bool, len(panePIDs))
	for _, pid := range panePIDs {
		wanted[pid] = true
	}

	processesByPID := make(map[string][]core.PaneProcess)
	for _, entry := range processes {
		if wanted[entry.parentPID] {
			processesByPID[entry.parentPID] = append(processesByPID[entry.parentPID], entry.process)
		}
	}
	return processesByPID
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

// createSession creates a session with its task and editor windows and
// returns the task window's pane, or a zero pane when tmux did not report it.
func (r *repository) createSession(ctx context.Context, sessionName, workingDir string) (core.TmuxPaneRef, error) {
	sessionName = normalizedSessionName(sessionName)

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-session",
		"-d",
		"-P",
		"-F",
		paneRefFormat,
		"-s",
		sessionName,
		"-n",
		taskWindowName,
		"-c",
		workingDir,
	)
	if err != nil {
		return core.TmuxPaneRef{}, err
	}
	pane, ok := parsePaneRef(result.Stdout)
	if !ok {
		// The task window is known by name, so the launch goes on without the
		// pane; its agent's first hook event places it instead.
		pane = core.TmuxPaneRef{}
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
		return pane, nil
	}

	_, cleanupErr := r.runner.Run(
		context.WithoutCancel(ctx),
		"",
		"tmux",
		"kill-session",
		"-t",
		exactSessionTarget(sessionName),
	)
	if cleanupErr != nil {
		return core.TmuxPaneRef{}, errors.Join(err, cleanupErr)
	}

	return core.TmuxPaneRef{}, err
}

// cleanupStartedSession kills a session StartTaskSession created but could not
// launch in. It runs even when the caller's context is done.
func (r *repository) cleanupStartedSession(ctx context.Context, sessionName string, cause error) error {
	_, cleanupErr := r.runner.Run(
		context.WithoutCancel(ctx),
		"",
		"tmux",
		"kill-session",
		"-t",
		exactSessionTarget(sessionName),
	)
	if cleanupErr != nil {
		return errors.Join(cause, cleanupErr)
	}

	return cause
}

// sendKeys types command into the target pane's shell and submits it.
func (r *repository) sendKeys(ctx context.Context, target string, command []string) error {
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
		target,
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

// exactSessionOnlyTarget names the session itself, and never a window or pane.
// Without the colon tmux tries a target as a window of the current session
// first, and reads a dot in the name as a window and pane separator.
func exactSessionOnlyTarget(session string) string {
	return exactSessionTarget(session) + ":"
}

func exactWindowTarget(session, window string) string {
	return "=" + normalizedSessionName(session) + ":" + window
}

func normalizedSessionName(session string) string {
	return strings.ReplaceAll(session, ":", "-")
}

// paneRefFormat reports a pane tmux creates: its ID and its server's
// identity, as paneInventoryFormat reports them.
const paneRefFormat = "#{pane_id}\t#{socket_path}\t#{pid}"

// parsePaneRef parses paneRefFormat output. The server stays zero when tmux
// did not expand its identity, as tmux before 3.2 does not. ok is false when
// the output is not paneRefFormat; the returned pane then has only the pane
// ID the output starts with, if any, so the caller can close its window.
func parsePaneRef(output string) (core.TmuxPaneRef, bool) {
	line, _, _ := strings.Cut(output, "\n")
	fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
	id := strings.TrimSpace(fields[0])
	if !strings.HasPrefix(id, "%") {
		return core.TmuxPaneRef{}, false
	}
	pane := core.TmuxPaneRef{ID: id}
	if len(fields) != 3 {
		return pane, false
	}
	pane.Server = tmuxServer(fields[1], fields[2])
	return pane, true
}

type paneInventoryEntry struct {
	server      core.TmuxServer
	session     string
	window      string
	id          string
	command     string
	pid         string
	windowIndex int
	paneIndex   int
}

// parsePaneInventory parses paneInventoryFormat output. A line missing its
// session, indexes or pane ID makes the whole inventory incomplete. Windows
// may have empty names, which rename-window accepts, and tmux versions
// without socket_path only leave the server identity unknown.
func parsePaneInventory(output string) ([]paneInventoryEntry, bool) {
	var inventory []paneInventoryEntry
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 9 {
			return nil, false
		}
		session := normalizedSessionName(strings.TrimSpace(fields[2]))
		window := strings.TrimSpace(fields[4])
		id := strings.TrimSpace(fields[6])
		windowIndex, windowIndexErr := strconv.Atoi(strings.TrimSpace(fields[3]))
		paneIndex, paneIndexErr := strconv.Atoi(strings.TrimSpace(fields[5]))
		if session == "" || id == "" || windowIndexErr != nil || paneIndexErr != nil {
			return nil, false
		}
		inventory = append(inventory, paneInventoryEntry{
			server:      tmuxServer(fields[0], fields[1]),
			session:     session,
			window:      window,
			id:          id,
			command:     strings.TrimSpace(fields[7]),
			pid:         strings.TrimSpace(fields[8]),
			windowIndex: windowIndex,
			paneIndex:   paneIndex,
		})
	}
	return inventory, true
}

// tmuxServer returns the identity of the server at socketPath with the given
// PID, or zero unless both are valid.
func tmuxServer(socketPath, pid string) core.TmuxServer {
	socketPath = strings.TrimSpace(socketPath)
	serverPID, err := strconv.Atoi(strings.TrimSpace(pid))
	if socketPath == "" || err != nil || serverPID <= 0 {
		return core.TmuxServer{}
	}
	return core.TmuxServer{SocketPath: socketPath, PID: serverPID}
}

func isMissingSessionError(err error, result subprocess.Result) bool {
	if err == nil {
		return false
	}

	lower := strings.ToLower(commandStderr(err, result))
	return strings.Contains(lower, "can't find session") ||
		strings.Contains(lower, "can't find window") ||
		strings.Contains(lower, "can't find pane")
}

// isMissingPaneError reports whether tmux failed because a pane target no
// longer exists.
// isMissingWindowError reports tmux finding a target's session but not its
// window.
func isMissingWindowError(err error, result subprocess.Result) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(commandStderr(err, result)), "can't find window")
}

func isMissingPaneError(err error, result subprocess.Result) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(commandStderr(err, result)), "can't find pane")
}

// isNoTmuxServerError reports whether tmux failed because no server is
// running, so no Session can exist. tmux says so in two ways: "error connecting
// to <socket> (No such file or directory)" when there is no socket file, as
// after a reboot, and "no server running on <socket>" when a socket file is
// left behind, as after a crash or kill-server. Other connection failures, such
// as Permission denied, are not a missing server. The parenthesised reason is
// strerror text, matched in English: macOS does not localize it, but glibc can
// translate it on Linux, and a translated reason surfaces as an ordinary error.
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
	return strings.Contains(stderr, "no server running") ||
		(strings.Contains(stderr, "error connecting to") && strings.Contains(stderr, "(no such file or directory)"))
}

// commandStderr returns a failed tmux command's stderr, from its result or,
// when the runner reported it only there, from its error.
func commandStderr(err error, result subprocess.Result) string {
	if strings.TrimSpace(result.Stderr) != "" {
		return result.Stderr
	}
	var commandErr subprocess.CommandError
	if errors.As(err, &commandErr) {
		return commandErr.Stderr
	}
	return ""
}
