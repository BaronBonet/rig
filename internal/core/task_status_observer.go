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

	persisted       *TaskStatusUpdate
	view            *TaskStatusUpdate
	viewObservedAt  time.Time
	lastCycleAt     time.Time
	generation      uint64
	persistedCancel context.CancelFunc

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
	taskID              string
	generation          uint64
	persisted           *TaskStatusUpdate
	task                *Task
	runtime             TaskSessionRuntimeState
	configuredProviders []Provider
}

type taskStatusCycleResult struct {
	taskID              string
	generation          uint64
	update              *TaskStatusUpdate
	expectedProvider    Provider
	replacementProvider Provider
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
	waiterID, err := o.addWaiter(taskID, waiter)
	if err != nil {
		return nil, err
	}
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
	state, err := o.ensurePersistedInterestLocked(taskID)
	if err != nil {
		o.mu.Unlock()
		return nil, err
	}
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

func (o *taskStatusObserver) addWaiter(taskID string, waiter chan taskStatusResult) (uint64, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	state, err := o.ensurePersistedInterestLocked(taskID)
	if err != nil {
		return 0, err
	}
	if !o.stateHasInterestLocked(state) && !o.cacheFreshLocked(state) {
		state.lastCycleAt = time.Time{}
	}
	o.nextID++
	waiterID := o.nextID
	state.waiters[waiterID] = waiter
	o.requestCycle()
	return waiterID, nil
}

func (o *taskStatusObserver) ensurePersistedInterestLocked(taskID string) (*observedTaskStatus, error) {
	state := o.tasks[taskID]
	if state == nil {
		state = &observedTaskStatus{
			taskID:      taskID,
			subscribers: make(map[uint64]*taskStatusSubscriber),
			waiters:     make(map[uint64]chan taskStatusResult),
		}
		o.tasks[taskID] = state
	}
	if state.persistedCancel != nil {
		return state, nil
	}

	persistedCtx, cancel := context.WithCancel(context.Background())
	updates, err := o.observation.tasks.SubscribeTaskStatus(persistedCtx, taskID)
	if err != nil {
		cancel()
		if len(state.subscribers) == 0 && len(state.waiters) == 0 && state.viewObservedAt.IsZero() {
			delete(o.tasks, taskID)
		}
		return nil, err
	}
	state.persistedCancel = cancel
	go o.forwardPersistedUpdates(persistedCtx, taskID, updates)
	return state, nil
}

func (o *taskStatusObserver) forwardPersistedUpdates(
	ctx context.Context,
	taskID string,
	updates <-chan TaskStatusUpdate,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case update, ok := <-updates:
			if !ok {
				o.persistedStreamClosed(ctx, taskID)
				return
			}
			o.acceptPersistedEvidence(taskID, update)
		}
	}
}

func (o *taskStatusObserver) acceptPersistedEvidence(taskID string, update TaskStatusUpdate) {
	o.mu.Lock()
	state := o.tasks[taskID]
	if state == nil || state.persistedCancel == nil {
		o.mu.Unlock()
		return
	}

	state.generation++
	state.persisted = cloneTaskStatusUpdate(&update)
	o.publishLocked(state, &update)
	o.resolveWaitersLocked(state, taskStatusResult{update: &update})
	o.releasePersistedInterestIfIdleLocked(state)
	o.mu.Unlock()
}

func (o *taskStatusObserver) persistedStreamClosed(streamCtx context.Context, taskID string) {
	if streamCtx.Err() != nil {
		return
	}

	o.mu.Lock()
	state := o.tasks[taskID]
	if state == nil || state.persistedCancel == nil {
		o.mu.Unlock()
		return
	}
	state.persistedCancel()
	state.persistedCancel = nil
	hasInterest := o.stateHasInterestLocked(state)
	o.mu.Unlock()
	if !hasInterest {
		return
	}
	o.schedulePersistedInterestRetry(taskID)
}

func (o *taskStatusObserver) schedulePersistedInterestRetry(taskID string) {
	time.AfterFunc(o.observation.recoveryPollInterval, func() {
		o.mu.Lock()
		state := o.tasks[taskID]
		var retry bool
		if state != nil && o.stateHasInterestLocked(state) && state.persistedCancel == nil {
			_, err := o.ensurePersistedInterestLocked(taskID)
			retry = err != nil
		}
		o.mu.Unlock()
		if retry {
			o.schedulePersistedInterestRetry(taskID)
		}
	})
}

func (o *taskStatusObserver) removeSubscriber(taskID string, subscriberID uint64) {
	o.mu.Lock()
	state := o.tasks[taskID]
	if state != nil {
		if subscriber := state.subscribers[subscriberID]; subscriber != nil {
			delete(state.subscribers, subscriberID)
			close(subscriber.updates)
		}
		o.releasePersistedInterestIfIdleLocked(state)
	}
	o.mu.Unlock()
	o.requestCycle()
}

func (o *taskStatusObserver) removeWaiter(taskID string, waiterID uint64) {
	o.mu.Lock()
	state := o.tasks[taskID]
	if state != nil {
		delete(state.waiters, waiterID)
		o.releasePersistedInterestIfIdleLocked(state)
	}
	o.mu.Unlock()
	o.requestCycle()
}

func (o *taskStatusObserver) releasePersistedInterestIfIdleLocked(state *observedTaskStatus) {
	if o.stateHasInterestLocked(state) || state.persistedCancel == nil {
		return
	}
	state.persistedCancel()
	state.persistedCancel = nil
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

	inputs := make([]taskStatusCycleInput, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		update, err := o.observation.tasks.LatestTaskStatus(context.Background(), taskID)
		if err != nil {
			o.resolveTaskWaiters(taskID, taskStatusResult{err: err})
			continue
		}
		input, interested := o.snapshotPersistedInput(taskID, update)
		if !interested {
			continue
		}
		if input.persisted == nil {
			o.resolveTaskWaiters(taskID, taskStatusResult{})
			continue
		}
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return
	}

	tasks, err := o.observation.tasks.ListTasks(context.Background())
	if err != nil {
		o.publishUnrecovered(inputs)
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
		input.task = task
		filtered = append(filtered, input)
		runtimeTasks = append(runtimeTasks, task)
	}
	inputs = filtered
	if len(inputs) == 0 {
		return
	}

	runtimeByTask, err := o.observation.tmuxSession.InspectTaskSessions(context.Background(), runtimeTasks)
	if err != nil {
		o.publishUnrecovered(inputs)
		return
	}
	configuredProviders := o.configuredProviders(context.Background())

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
				runtime, ok := runtimeByTask[input.taskID]
				if !ok {
					results <- taskStatusCycleResult{
						taskID:     input.taskID,
						generation: input.generation,
						update:     input.persisted,
					}
					continue
				}
				input.runtime = runtime
				input.configuredProviders = configuredProviders
				results <- o.recoverCurrentStatus(context.Background(), input)
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
		o.acceptCycleResult(result)
	}
}

func (o *taskStatusObserver) configuredProviders(ctx context.Context) []Provider {
	if o.observation.providerConfig == nil {
		return nil
	}
	setup, err := o.observation.providerConfig.GetProviderSetup(ctx)
	if err != nil || setup == nil {
		return nil
	}
	return configuredProvidersInOrder(*setup)
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

func (o *taskStatusObserver) snapshotPersistedInput(
	taskID string,
	latest *TaskStatusUpdate,
) (taskStatusCycleInput, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	state := o.tasks[taskID]
	if !o.stateHasInterestLocked(state) {
		return taskStatusCycleInput{}, false
	}
	if shouldAcceptLatestPersisted(state.persisted, latest) {
		state.generation++
		state.persisted = cloneTaskStatusUpdate(latest)
	}
	return taskStatusCycleInput{
		taskID:     taskID,
		generation: state.generation,
		persisted:  cloneTaskStatusUpdate(state.persisted),
	}, true
}

func shouldAcceptLatestPersisted(current, latest *TaskStatusUpdate) bool {
	if latest == nil {
		return false
	}
	if current == nil {
		return true
	}
	if latest.ObservedAt.After(current.ObservedAt) {
		return true
	}
	return latest.ObservedAt.Equal(current.ObservedAt) && !taskStatusUpdatesEqual(current, latest)
}

func (o *taskStatusObserver) publishUnrecovered(inputs []taskStatusCycleInput) {
	for _, input := range inputs {
		o.acceptCycleResult(taskStatusCycleResult{
			taskID:     input.taskID,
			generation: input.generation,
			update:     input.persisted,
		})
	}
}

func (o *taskStatusObserver) recoverCurrentStatus(
	ctx context.Context,
	input taskStatusCycleInput,
) taskStatusCycleResult {
	result := taskStatusCycleResult{
		taskID:     input.taskID,
		generation: input.generation,
		update:     input.persisted,
	}
	if replacement := replacementActiveProvider(
		input.runtime,
		input.task.Provider,
		input.configuredProviders,
		o.observation.providers,
	); replacement != "" {
		result.expectedProvider = input.task.Provider
		result.replacementProvider = replacement
		return result
	}

	update := input.persisted
	if update == nil || update.Phase == TaskStatusPhaseStopped {
		return result
	}

	providerClient, err := supportedProviderClient(o.observation.providers, input.task.Provider)
	if err != nil {
		return result
	}

	switch resolveStatus(update, input.runtime, providerClient.TaskSessionCommandName()) {
	case statusTryRecover:
		sessions, listErr := o.observation.tasks.ListTaskProviderSessions(ctx, update.TaskID)
		if listErr != nil {
			return result
		}
		recovered, recoverErr := providerClient.RecoverLatestTaskStatus(ctx, *update, sessions)
		if recoverErr != nil || recovered == nil {
			return result
		}
		if recovered.ObservedAt.Before(update.ObservedAt) {
			return result
		}
		result.update = recovered
	case statusStopped:
		stopped := *update
		stopped.Phase = TaskStatusPhaseStopped
		stopped.RawEventName = "TaskSessionStopped"
		result.update = &stopped
	}
	return result
}

func (o *taskStatusObserver) acceptCycleResult(result taskStatusCycleResult) {
	o.mu.Lock()
	state := o.tasks[result.taskID]
	if state == nil || !o.stateHasInterestLocked(state) || state.generation != result.generation {
		o.mu.Unlock()
		return
	}
	if result.replacementProvider != "" {
		// Reserve a generation before leaving the observer lock. Hook evidence
		// accepted after the runtime snapshot then invalidates this reconciliation
		// instead of letting an older tmux view overwrite newer provider evidence.
		state.generation++
		reservedGeneration := state.generation
		o.mu.Unlock()

		adopted := o.reconcileActiveProvider(context.Background(), result)

		o.mu.Lock()
		state = o.tasks[result.taskID]
		if state == nil || !o.stateHasInterestLocked(state) || state.generation != reservedGeneration {
			o.mu.Unlock()
			return
		}
		if adopted && result.update != nil {
			result.update = cloneTaskStatusUpdate(result.update)
			result.update.Provider = result.replacementProvider
		}
	}
	o.publishLocked(state, result.update)
	o.resolveWaitersLocked(state, taskStatusResult{update: result.update})
	o.releasePersistedInterestIfIdleLocked(state)
	o.mu.Unlock()
}

func (o *taskStatusObserver) reconcileActiveProvider(ctx context.Context, result taskStatusCycleResult) bool {
	task, err := taskByID(ctx, o.observation.tasks, result.taskID)
	if err != nil || task.Provider != result.expectedProvider {
		return false
	}
	_, err = recordActiveProvider(ctx, o.observation.tasks, task, result.replacementProvider)
	return err == nil
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
	copyUpdate := *update
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
	subscriber.lastOffered = &copyUpdate
}

func (o *taskStatusObserver) resolveTaskWaiters(taskID string, result taskStatusResult) {
	o.mu.Lock()
	state := o.tasks[taskID]
	if state != nil {
		o.resolveWaitersLocked(state, result)
		o.releasePersistedInterestIfIdleLocked(state)
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
	if state.persistedCancel != nil {
		state.persistedCancel()
		state.persistedCancel = nil
	}
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
	return &copyUpdate
}
