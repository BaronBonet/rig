package core

import (
	"context"
	"strings"
	"sync"
	"time"
)

// taskStatusObserver coordinates every live task-status interest owned by one
// daemon-side task service. Its public seam remains the per-task methods on
// TaskService; batching, scheduling, recovery limits, and conflation stay here.
type taskStatusObserver struct {
	observation *taskObservation

	mu        sync.Mutex
	tasks     map[string]*observedTaskStatus
	nextID    uint64
	wakeCycle chan struct{}
}

type observedTaskStatus struct {
	taskID string

	view           *TaskStatusUpdate
	viewObservedAt time.Time
	lastCycleAt    time.Time
	// generation advances whenever hook evidence changes the task's agent
	// sessions, so a cycle that started before it cannot publish over it.
	generation uint64
	// liveView is what the last cycle learned beyond the hook evidence.
	liveView agentSessionLiveView

	subscribers map[uint64]*taskStatusSubscriber
	waiters     map[uint64]chan taskStatusResult
}

type taskStatusSubscriber struct {
	updates     chan TaskStatusUpdate
	lastOffered *TaskStatusUpdate
}

type taskStatusResult struct {
	update *TaskStatusUpdate
	err    error
}

type taskStatusCycleInput struct {
	taskID     string
	generation uint64
	evidence   agentSessionEvidence
	liveView   agentSessionLiveView
}

type taskStatusCycleResult struct {
	taskID     string
	generation uint64
	update     *TaskStatusUpdate
	liveView   agentSessionLiveView
}

func newTaskStatusObserver(observation *taskObservation) *taskStatusObserver {
	observer := &taskStatusObserver{
		observation: observation,
		tasks:       make(map[string]*observedTaskStatus),
		wakeCycle:   make(chan struct{}, 1),
	}
	go observer.run()
	return observer
}

func (o *taskStatusObserver) LatestTaskStatus(
	ctx context.Context,
	taskID string,
) (*TaskStatusUpdate, error) {
	taskID = strings.TrimSpace(taskID)
	if cached, ok := o.freshCachedView(taskID); ok {
		return cached, nil
	}

	waiter := make(chan taskStatusResult, 1)
	waiterID := o.addWaiter(taskID, waiter)
	defer o.removeWaiter(taskID, waiterID)

	select {
	case result := <-waiter:
		return cloneTaskStatusUpdate(result.update), result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (o *taskStatusObserver) SubscribeTaskStatus(
	ctx context.Context,
	taskID string,
) (<-chan TaskStatusUpdate, error) {
	taskID = strings.TrimSpace(taskID)
	subscriber := &taskStatusSubscriber{updates: make(chan TaskStatusUpdate, 1)}

	o.mu.Lock()
	state := o.ensureStateLocked(taskID)
	if !o.stateHasInterestLocked(state) && !o.cacheFreshLocked(state) {
		state.lastCycleAt = time.Time{}
	}
	o.nextID++
	subscriberID := o.nextID
	state.subscribers[subscriberID] = subscriber
	if o.cacheFreshLocked(state) {
		o.offerLocked(subscriber, state.view)
	}
	o.mu.Unlock()

	o.requestCycle()
	go func() {
		<-ctx.Done()
		o.removeSubscriber(taskID, subscriberID)
	}()
	return subscriber.updates, nil
}

func (o *taskStatusObserver) ForgetTask(taskID string) {
	taskID = strings.TrimSpace(taskID)
	o.mu.Lock()
	state := o.tasks[taskID]
	if state != nil {
		delete(o.tasks, taskID)
		o.closeStateLocked(state, ErrTaskNotFound)
	}
	o.mu.Unlock()
	o.requestCycle()
}

// agentSessionsChanged publishes a subscribed task's Runtime status again
// after hook evidence changed its agent sessions, with the last cycle's live
// view. Callers hold the observation's agent session lock, so hook evidence
// publishes in the order it was recorded. Waiting one-shot reads are left to
// the cycle they wait for, which checks liveness.
func (o *taskStatusObserver) agentSessionsChanged(ctx context.Context, taskID string) {
	o.mu.Lock()
	state := o.tasks[taskID]
	if state == nil {
		o.mu.Unlock()
		return
	}
	state.generation++
	if len(state.subscribers) == 0 {
		// The cached view is stale: the next read, or the cycle a waiting read
		// waits for, rebuilds it.
		state.viewObservedAt = time.Time{}
		o.mu.Unlock()
		return
	}
	o.mu.Unlock()

	evidence, err := o.observation.readAgentSessionEvidence(ctx, taskID)

	o.mu.Lock()
	defer o.mu.Unlock()
	state = o.tasks[taskID]
	if state == nil {
		return
	}
	if err != nil || len(state.subscribers) == 0 {
		state.viewObservedAt = time.Time{}
		return
	}
	o.publishLocked(state, taskStatusView(taskID, evidence, state.liveView))
}

func (o *taskStatusObserver) freshCachedView(taskID string) (*TaskStatusUpdate, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	state := o.tasks[taskID]
	if state == nil || !o.cacheFreshLocked(state) {
		return nil, false
	}
	return cloneTaskStatusUpdate(state.view), true
}

func (o *taskStatusObserver) cacheFreshLocked(state *observedTaskStatus) bool {
	if state == nil || state.viewObservedAt.IsZero() {
		return false
	}
	maxAge := o.observation.statusCacheMaxAge
	if maxAge <= 0 {
		maxAge = o.observation.recoveryPollInterval
	}
	return time.Since(state.viewObservedAt) <= maxAge
}

func (o *taskStatusObserver) addWaiter(taskID string, waiter chan taskStatusResult) uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()

	state := o.ensureStateLocked(taskID)
	if !o.stateHasInterestLocked(state) && !o.cacheFreshLocked(state) {
		state.lastCycleAt = time.Time{}
	}
	o.nextID++
	waiterID := o.nextID
	state.waiters[waiterID] = waiter
	o.requestCycle()
	return waiterID
}

func (o *taskStatusObserver) ensureStateLocked(taskID string) *observedTaskStatus {
	state := o.tasks[taskID]
	if state == nil {
		state = &observedTaskStatus{
			taskID:      taskID,
			liveView:    newAgentSessionLiveView(),
			subscribers: make(map[uint64]*taskStatusSubscriber),
			waiters:     make(map[uint64]chan taskStatusResult),
		}
		o.tasks[taskID] = state
	}
	return state
}

func (o *taskStatusObserver) removeSubscriber(taskID string, subscriberID uint64) {
	o.mu.Lock()
	state := o.tasks[taskID]
	if state != nil {
		if subscriber := state.subscribers[subscriberID]; subscriber != nil {
			delete(state.subscribers, subscriberID)
			close(subscriber.updates)
		}
	}
	o.mu.Unlock()
	o.requestCycle()
}

func (o *taskStatusObserver) removeWaiter(taskID string, waiterID uint64) {
	o.mu.Lock()
	state := o.tasks[taskID]
	if state != nil {
		delete(state.waiters, waiterID)
	}
	o.mu.Unlock()
	o.requestCycle()
}

func (o *taskStatusObserver) stateHasInterestLocked(state *observedTaskStatus) bool {
	return state != nil && (len(state.subscribers) > 0 || len(state.waiters) > 0)
}

func (o *taskStatusObserver) requestCycle() {
	select {
	case o.wakeCycle <- struct{}{}:
	default:
	}
}

func (o *taskStatusObserver) run() {
	for range o.wakeCycle {
		if !o.hasInterest() {
			continue
		}

		for {
			o.runCycle()
			if o.hasNeverObservedInterest() {
				continue
			}

			interval := o.observation.recoveryPollInterval
			if interval <= 0 {
				interval = time.Nanosecond
			}
			timer := time.NewTimer(interval)
			stop := false
			for !stop {
				select {
				case <-timer.C:
					stop = true
				case <-o.wakeCycle:
					if !o.hasInterest() || o.hasNeverObservedInterest() {
						if !timer.Stop() {
							select {
							case <-timer.C:
							default:
							}
						}
						stop = true
					}
				}
			}
			if !o.hasInterest() {
				break
			}
		}
	}
}

func (o *taskStatusObserver) hasInterest() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, state := range o.tasks {
		if o.stateHasInterestLocked(state) {
			return true
		}
	}
	return false
}

func (o *taskStatusObserver) hasNeverObservedInterest() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, state := range o.tasks {
		if o.stateHasInterestLocked(state) && state.lastCycleAt.IsZero() {
			return true
		}
	}
	return false
}

func (o *taskStatusObserver) runCycle() {
	taskIDs := o.interestedTaskIDs()
	if len(taskIDs) == 0 {
		return
	}

	ctx := context.Background()
	inputs := make([]taskStatusCycleInput, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		// The generation is taken before the evidence is read, so hook evidence
		// recorded after this point discards the cycle's result.
		input, interested := o.cycleInput(taskID)
		if !interested {
			continue
		}
		evidence, err := o.observation.readAgentSessionEvidence(ctx, taskID)
		if err != nil {
			o.resolveTaskWaiters(taskID, taskStatusResult{err: err})
			continue
		}
		if evidence.empty() {
			// A task without agent sessions has no status, unless a hook
			// recorded its first one meanwhile.
			o.acceptCycleResult(ctx, taskStatusCycleResult{
				taskID:     taskID,
				generation: input.generation,
				liveView:   input.liveView,
			})
			continue
		}
		input.evidence = evidence
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return
	}

	tasks, err := o.observation.tasks.ListTasks(ctx)
	if err != nil {
		o.publishUnobserved(ctx, inputs)
		return
	}
	taskByID := make(map[string]*Task, len(tasks))
	for _, task := range tasks {
		if task != nil {
			taskByID[strings.TrimSpace(task.ID)] = task
		}
	}

	runtimeTasks := make([]*Task, 0, len(inputs))
	filtered := inputs[:0]
	for _, input := range inputs {
		task := taskByID[input.taskID]
		if task == nil {
			o.ForgetTask(input.taskID)
			continue
		}
		filtered = append(filtered, input)
		runtimeTasks = append(runtimeTasks, task)
	}
	inputs = filtered
	if len(inputs) == 0 {
		return
	}

	snapshot, err := o.observation.tmuxSession.InspectTaskSessions(ctx, runtimeTasks)
	if err != nil {
		o.publishUnobserved(ctx, inputs)
		return
	}
	panes := make(map[string]TmuxPane, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		panes[pane.ID] = pane
	}

	jobs := make(chan taskStatusCycleInput)
	results := make(chan taskStatusCycleResult, len(inputs))
	workerLimit := o.observation.recoveryWorkerLimit
	if workerLimit <= 0 {
		workerLimit = defaultTaskStatusRecoveryWorkerLimit
	}
	if workerLimit > len(inputs) {
		workerLimit = len(inputs)
	}

	var workers sync.WaitGroup
	workers.Add(workerLimit)
	for range workerLimit {
		go func() {
			defer workers.Done()
			for input := range jobs {
				results <- o.observeTaskStatus(ctx, input, snapshot, panes)
			}
		}()
	}
	go func() {
		for _, input := range inputs {
			jobs <- input
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	for result := range results {
		o.acceptCycleResult(ctx, result)
	}
}

func (o *taskStatusObserver) interestedTaskIDs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	taskIDs := make([]string, 0, len(o.tasks))
	for taskID, state := range o.tasks {
		if o.stateHasInterestLocked(state) {
			taskIDs = append(taskIDs, taskID)
			state.lastCycleAt = time.Now()
		}
	}
	return taskIDs
}

func (o *taskStatusObserver) cycleInput(taskID string) (taskStatusCycleInput, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	state := o.tasks[taskID]
	if !o.stateHasInterestLocked(state) {
		return taskStatusCycleInput{}, false
	}
	return taskStatusCycleInput{
		taskID:     taskID,
		generation: state.generation,
		liveView:   state.liveView,
	}, true
}

// publishUnobserved publishes views built from hook evidence and the previous
// live view when the cycle cannot inspect the tasks' Sessions.
func (o *taskStatusObserver) publishUnobserved(ctx context.Context, inputs []taskStatusCycleInput) {
	for _, input := range inputs {
		o.acceptCycleResult(ctx, taskStatusCycleResult{
			taskID:     input.taskID,
			generation: input.generation,
			update:     taskStatusView(input.taskID, input.evidence, input.liveView),
			liveView:   input.liveView,
		})
	}
}

// observeTaskStatus checks one task's open agent sessions against the tmux
// snapshot. It ends those whose agent exited or whose pane closed, keeps lost
// ones open for Reconnect, recovers stale status for live ones from
// provider-side state, and builds the task's Runtime status from the rest.
func (o *taskStatusObserver) observeTaskStatus(
	ctx context.Context,
	input taskStatusCycleInput,
	snapshot TmuxSnapshot,
	panes map[string]TmuxPane,
) taskStatusCycleResult {
	sessionExists := len(snapshot.TaskSessionPanes[input.taskID]) > 0
	liveView := newAgentSessionLiveView()
	var history []TaskProviderSession
	historyRead := false

	open := make([]AgentSession, 0, len(input.evidence.open))
	for _, session := range input.evidence.open {
		var pane *TmuxPane
		if found, ok := panes[session.TmuxPane]; ok && session.TmuxPane != "" {
			pane = &found
		}
		providerClient, providerErr := supportedProviderClient(o.observation.providers, session.Provider)
		providerCommand := ""
		if providerErr == nil {
			providerCommand = providerClient.TaskSessionCommandName()
		}

		// The location is kept for an agent session this cycle ends too: a hook
		// may have changed it since, so it stays open and the rebased view
		// still shows where it runs.
		if pane != nil && (session.TmuxServer.IsZero() || session.TmuxServer == snapshot.Server) {
			liveView.locations[session.ID] = paneLocation{
				pane: session.paneRef(),
				location: TmuxPaneLocation{
					WindowName:  pane.WindowName,
					WindowIndex: pane.WindowIndex,
					PaneIndex:   pane.PaneIndex,
				},
			}
		} else if located, ok := input.liveView.locations[session.ID]; ok {
			liveView.locations[session.ID] = located
		}

		liveness := resolveAgentSessionLiveness(
			session,
			sessionExists,
			pane,
			snapshot.Server,
			providerCommand,
			o.observation.now(),
		)
		switch liveness {
		case agentSessionEnded:
			// An agent session that a hook changed meanwhile, or that fails to
			// end, stays open in the repository. A hook change also changes
			// the generation, so the result is rebased onto it.
			_ = o.observation.endAgentSession(ctx, session)
			continue
		case agentSessionLost:
			liveView.lost[session.ID] = session.paneRef()
		case agentSessionLaunching:
			// Its first hook brings its status; until then it is starting, and
			// its pane has nothing to recover.
		case agentSessionUnchanged:
			if input.liveView.isLost(session) {
				liveView.lost[session.ID] = session.paneRef()
			} else if recovered, ok := input.liveView.recovered[session.ID]; ok && recovered.appliesTo(session) {
				liveView.recovered[session.ID] = recovered
			}
		case agentSessionLive:
			if !historyRead {
				history, _ = o.observation.tasks.ListTaskProviderSessions(ctx, input.taskID)
				historyRead = true
			}
			recovered := recoverAgentSessionStatus(ctx, providerClient, session, history, pane, providerCommand)
			if recovered != nil {
				liveView.recovered[session.ID] = *recovered
			}
		}
		open = append(open, session)
	}

	evidence := agentSessionEvidence{open: open, latest: input.evidence.latest, prompts: input.evidence.prompts}
	return taskStatusCycleResult{
		taskID:     input.taskID,
		generation: input.generation,
		update:     taskStatusView(input.taskID, evidence, liveView),
		liveView:   liveView,
	}
}

// recoverAgentSessionStatus asks the agent session's provider for a newer
// status than its hook-driven one, from the transcripts of its current
// Provider session only. A recovery older than the hook evidence is
// discarded.
func recoverAgentSessionStatus(
	ctx context.Context,
	providerClient ProviderClient,
	session AgentSession,
	history []TaskProviderSession,
	pane *TmuxPane,
	providerCommand string,
) *recoveredAgentSessionStatus {
	conversation := conversationHistory(history, session)
	if providerClient == nil || len(conversation) == 0 {
		return nil
	}
	recovered, err := providerClient.RecoverAgentSessionStatus(
		ctx,
		session.Status,
		conversation,
		paneProviderStartedAt(pane, providerCommand),
	)
	if err != nil || recovered == nil || recovered.ObservedAt.Before(session.Status.ObservedAt) {
		return nil
	}
	return &recoveredAgentSessionStatus{
		pane:              session.paneRef(),
		providerSessionID: session.ProviderSessionID,
		from:              session.Status,
		status:            *recovered,
	}
}

func (o *taskStatusObserver) acceptCycleResult(ctx context.Context, result taskStatusCycleResult) {
	o.mu.Lock()
	state := o.tasks[result.taskID]
	if state == nil || !o.stateHasInterestLocked(state) {
		o.mu.Unlock()
		return
	}
	if state.generation == result.generation {
		o.applyCycleLocked(state, result.liveView, result.update)
		o.mu.Unlock()
		return
	}
	o.mu.Unlock()
	o.rebaseCycleResult(ctx, result)
}

// rebaseCycleResult applies the live view of a cycle that hook evidence
// overlapped to that newer evidence, rather than discarding the cycle and
// leaving waiting reads with a view that skipped liveness. Holding the agent
// session lock orders the rebased view after every hook's publish, so it
// cannot overwrite newer evidence. The live view carries over safely: a
// recovered status applies only while the status it came from is current,
// agent sessions a hook opened are not in it, and those the cycle ended are
// gone from the evidence.
func (o *taskStatusObserver) rebaseCycleResult(ctx context.Context, result taskStatusCycleResult) {
	o.observation.agentSessionMu.Lock()
	defer o.observation.agentSessionMu.Unlock()
	evidence, err := o.observation.readAgentSessionEvidence(ctx, result.taskID)

	o.mu.Lock()
	defer o.mu.Unlock()
	state := o.tasks[result.taskID]
	if state == nil || !o.stateHasInterestLocked(state) {
		return
	}
	if err != nil {
		o.resolveWaitersLocked(state, taskStatusResult{err: err})
		return
	}
	o.applyCycleLocked(state, result.liveView, taskStatusView(result.taskID, evidence, result.liveView))
}

func (o *taskStatusObserver) applyCycleLocked(
	state *observedTaskStatus,
	liveView agentSessionLiveView,
	update *TaskStatusUpdate,
) {
	state.liveView = liveView
	o.publishLocked(state, update)
	o.resolveWaitersLocked(state, taskStatusResult{update: update})
}

func (o *taskStatusObserver) publishLocked(state *observedTaskStatus, update *TaskStatusUpdate) {
	if update == nil {
		return
	}
	state.view = cloneTaskStatusUpdate(update)
	state.viewObservedAt = time.Now()
	for _, subscriber := range state.subscribers {
		o.offerLocked(subscriber, update)
	}
}

func (o *taskStatusObserver) offerLocked(subscriber *taskStatusSubscriber, update *TaskStatusUpdate) {
	if subscriber == nil || update == nil || taskStatusUpdatesEqual(subscriber.lastOffered, update) {
		return
	}
	// The subscriber's copy shares nothing with the one kept for dedupe.
	copyUpdate := *cloneTaskStatusUpdate(update)
	select {
	case subscriber.updates <- copyUpdate:
	default:
		select {
		case <-subscriber.updates:
		default:
		}
		select {
		case subscriber.updates <- copyUpdate:
		default:
		}
	}
	subscriber.lastOffered = cloneTaskStatusUpdate(update)
}

func (o *taskStatusObserver) resolveTaskWaiters(taskID string, result taskStatusResult) {
	o.mu.Lock()
	state := o.tasks[taskID]
	if state != nil {
		o.resolveWaitersLocked(state, result)
	}
	o.mu.Unlock()
}

func (o *taskStatusObserver) resolveWaitersLocked(state *observedTaskStatus, result taskStatusResult) {
	for waiterID, waiter := range state.waiters {
		waiter <- taskStatusResult{
			update: cloneTaskStatusUpdate(result.update),
			err:    result.err,
		}
		delete(state.waiters, waiterID)
	}
}

func (o *taskStatusObserver) closeStateLocked(state *observedTaskStatus, cause error) {
	for _, subscriber := range state.subscribers {
		close(subscriber.updates)
	}
	for _, waiter := range state.waiters {
		waiter <- taskStatusResult{err: cause}
	}
	state.subscribers = nil
	state.waiters = nil
}

func cloneTaskStatusUpdate(update *TaskStatusUpdate) *TaskStatusUpdate {
	if update == nil {
		return nil
	}
	copyUpdate := *update
	copyUpdate.AgentSessions = cloneLiveAgentSessions(update.AgentSessions)
	return &copyUpdate
}

func cloneLiveAgentSessions(sessions []LiveAgentSession) []LiveAgentSession {
	if sessions == nil {
		return nil
	}
	cloned := make([]LiveAgentSession, len(sessions))
	for i, session := range sessions {
		if session.Location != nil {
			location := *session.Location
			session.Location = &location
		}
		cloned[i] = session
	}
	return cloned
}
