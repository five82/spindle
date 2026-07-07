package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/stage"
)

// PipelineStage describes a single stage in the pipeline.
// Stage identifies which queue stage this handler owns: the handler picks up
// items whose item.Stage equals this value, and on success advances the item
// to the next handler's Stage (or StageCompleted for the last handler).
// Claims declare the resources the stage consumes while running; the
// scheduler admits a task only when every claimed resource has capacity.
type PipelineStage struct {
	Handler stage.Handler
	Stage   queue.Stage
	// Claims declares every resource this stage may consume; capacities are
	// registered from it. When ClaimsFunc is nil, Claims is also what each
	// dispatch reserves.
	Claims map[string]int
	// ClaimsFunc, when set, picks the per-item subset of Claims to reserve
	// at dispatch time (e.g. the encode tier slot matching the item's
	// resolution). It must return claims whose resources appear in Claims.
	ClaimsFunc func(*queue.Item) map[string]int
	// DependsOn names the stages whose tasks must complete before this
	// stage's task is ready. Empty means: depend on the previously
	// registered stage (linear default); the first stage is a root.
	DependsOn []queue.Stage
}

// pipelineState holds runtime state for the pipeline.
type pipelineState struct {
	stages     []PipelineStage
	stageOrder []queue.Stage
	stageMap   map[queue.Stage]int
	specs      []queue.TaskSpec
	logger     *slog.Logger
}

// Manager runs the task scheduler.
type Manager struct {
	store                  *queue.Store
	notifier               *notify.Notifier
	pipeline               *pipelineState
	statusTracker          *httpapi.StatusTracker
	queueNotifyMu          sync.Mutex
	queueCycleActive       bool
	persistenceFailures    chan error
	persistenceFailureOnce sync.Once

	wake          chan struct{}
	budgetMu      sync.Mutex
	budgetCap     map[string]int
	budgetUsed    map[string]int
	budgetHolders map[string][]httpapi.ResourceHolder

	// running tracks the cancel function of each item's active workers,
	// keyed by item then task. It guards against dispatching work for an
	// item whose previous worker has not exited (StopItems clears
	// in_progress while the worker is still alive), and lets the scheduler
	// cancel workers of user-stopped items. Until Phase 4b's DAG templates,
	// dispatch policy allows at most one live worker per item.
	runningMu sync.Mutex
	running   map[int64]map[int64]context.CancelFunc

	// blocked tracks when each ready task first failed to reserve its
	// resource claims, so resource waits are logged once on entry and once
	// with the wait duration on grant, not on every scheduler pass.
	blockedMu sync.Mutex
	blocked   map[int64]time.Time
}

// New creates a workflow manager. statusTracker may be nil.
func New(store *queue.Store, notifier *notify.Notifier, statusTracker *httpapi.StatusTracker, logger *slog.Logger) *Manager {
	return &Manager{
		store:               store,
		notifier:            notifier,
		statusTracker:       statusTracker,
		persistenceFailures: make(chan error, 1),
		wake:                make(chan struct{}, 1),
		running:             make(map[int64]map[int64]context.CancelFunc),
		blocked:             make(map[int64]time.Time),
		pipeline: &pipelineState{
			logger: logs.Default(logger),
		},
	}
}

// ConfigureStages registers an ordered slice of stage handlers. Registered
// stages must appear in queue.StageOrder, in the same relative order: the
// stage enumeration is single-sourced there, and drifting from it would
// silently break retry validation and audit phase gates. Violations are
// programmer errors in a hardcoded template, so they panic at startup.
func (m *Manager) ConfigureStages(stages []PipelineStage) {
	position := make(map[queue.Stage]int, len(queue.StageOrder))
	for i, s := range queue.StageOrder {
		position[s] = i
	}
	prev := -1
	for _, s := range stages {
		pos, ok := position[s.Stage]
		if !ok {
			panic(fmt.Sprintf("workflow: stage %q is not declared in queue.StageOrder", s.Stage))
		}
		if pos <= prev {
			panic(fmt.Sprintf("workflow: stage %q registered out of queue.StageOrder order", s.Stage))
		}
		prev = pos
	}

	p := m.pipeline
	p.stages = stages
	p.stageMap = make(map[queue.Stage]int, len(stages))

	for i, s := range stages {
		p.stageMap[s.Stage] = i
	}

	p.stageOrder = make([]queue.Stage, len(stages))
	for i, s := range stages {
		p.stageOrder[i] = s.Stage
	}

	// Task specs mirror registration order. Stages with explicit DependsOn
	// keep their declared edges (DAG); stages without default to depending
	// on the previously registered stage.
	p.specs = make([]queue.TaskSpec, len(stages))
	for i, s := range stages {
		spec := queue.TaskSpec{Type: s.Stage, DependsOn: s.DependsOn}
		if len(spec.DependsOn) == 0 && i > 0 {
			spec.DependsOn = []queue.Stage{stages[i-1].Stage}
		}
		p.specs[i] = spec
	}

	// Budget capacities replicate today's exclusivity: every claimed
	// resource has capacity 1. Raising a capacity is a Phase 4/5 change
	// gated by the GPU coexistence validation (see the plan doc).
	m.budgetCap = make(map[string]int)
	m.budgetUsed = make(map[string]int)
	m.budgetHolders = make(map[string][]httpapi.ResourceHolder)
	for _, s := range stages {
		for res := range s.Claims {
			m.budgetCap[res] = 1
		}
	}
}

// reserve attempts to claim the stage's resources for a task. It is
// all-or-nothing; holders are recorded for the status API.
func (m *Manager) reserve(claims map[string]int, holder httpapi.ResourceHolder) bool {
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	for res, n := range claims {
		if m.budgetUsed[res]+n > m.budgetCap[res] {
			return false
		}
	}
	for res, n := range claims {
		m.budgetUsed[res] += n
		m.budgetHolders[res] = append(m.budgetHolders[res], holder)
	}
	return true
}

// release returns a task's claimed resources to the budget.
func (m *Manager) release(claims map[string]int, holder httpapi.ResourceHolder) {
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	for res, n := range claims {
		m.budgetUsed[res] -= n
		holders := m.budgetHolders[res]
		for i, h := range holders {
			if h == holder {
				m.budgetHolders[res] = append(holders[:i], holders[i+1:]...)
				break
			}
		}
	}
}

// PipelineInfo describes the registered template for the status API, with
// linear-default dependencies already resolved, so clients render the DAG
// data-driven instead of hardcoding it.
func (m *Manager) PipelineInfo() []httpapi.PipelineStageInfo {
	p := m.pipeline
	info := make([]httpapi.PipelineStageInfo, len(p.stages))
	for i, s := range p.stages {
		deps := make([]string, 0, len(p.specs[i].DependsOn))
		for _, dep := range p.specs[i].DependsOn {
			deps = append(deps, string(dep))
		}
		claims := make([]string, 0, len(s.Claims))
		for res := range s.Claims {
			claims = append(claims, res)
		}
		sort.Strings(claims)
		info[i] = httpapi.PipelineStageInfo{
			Stage:     string(s.Stage),
			DependsOn: deps,
			Claims:    claims,
		}
	}
	return info
}

// SchedulerSnapshot reports resource occupancy for the status API.
func (m *Manager) SchedulerSnapshot() map[string]httpapi.ResourceStatus {
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	snap := make(map[string]httpapi.ResourceStatus, len(m.budgetCap))
	for res, capacity := range m.budgetCap {
		holders := make([]httpapi.ResourceHolder, len(m.budgetHolders[res]))
		copy(holders, m.budgetHolders[res])
		snap[res] = httpapi.ResourceStatus{
			Capacity: capacity,
			Used:     m.budgetUsed[res],
			Holders:  holders,
		}
	}
	return snap
}

// signalWake nudges the scheduler loop without blocking.
func (m *Manager) signalWake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// trackWorker registers a task worker's cancel function under its item.
// Returns false only when that task already has a live worker.
func (m *Manager) trackWorker(itemID, taskID int64, cancel context.CancelFunc) bool {
	m.runningMu.Lock()
	defer m.runningMu.Unlock()
	if m.running[itemID] == nil {
		m.running[itemID] = make(map[int64]context.CancelFunc)
	}
	if _, live := m.running[itemID][taskID]; live {
		return false
	}
	m.running[itemID][taskID] = cancel
	return true
}

// untrackWorker removes a task worker's registration.
func (m *Manager) untrackWorker(itemID, taskID int64) {
	m.runningMu.Lock()
	defer m.runningMu.Unlock()
	delete(m.running[itemID], taskID)
	if len(m.running[itemID]) == 0 {
		delete(m.running, itemID)
	}
}

// hasLiveWorker reports whether the item has any worker that has not exited.
func (m *Manager) hasLiveWorker(itemID int64) bool {
	m.runningMu.Lock()
	defer m.runningMu.Unlock()
	return len(m.running[itemID]) > 0
}

// hasStaleWorker reports whether the item has a live worker whose task no
// longer exists in the item's current task rows (a cancelled worker still
// exiting after a retry recompiled the tasks).
func (m *Manager) hasStaleWorker(itemID int64) bool {
	m.runningMu.Lock()
	taskIDs := make([]int64, 0, len(m.running[itemID]))
	for id := range m.running[itemID] {
		taskIDs = append(taskIDs, id)
	}
	m.runningMu.Unlock()
	if len(taskIDs) == 0 {
		return false
	}
	tasks, err := m.store.TasksForItem(itemID)
	if err != nil {
		return true // fail safe: do not dispatch on unknown state
	}
	current := make(map[int64]bool, len(tasks))
	for _, t := range tasks {
		current[t.ID] = true
	}
	for _, id := range taskIDs {
		if !current[id] {
			return true
		}
	}
	return false
}

// cancelStoppedWorkers cancels the workers of items the user has stopped.
// Without this, `queue cancel` only flips queue state and the stage worker
// keeps running as a zombie -- and a subsequent retry can re-run earlier
// stages (staging wipe) underneath it.
func (m *Manager) cancelStoppedWorkers() {
	m.runningMu.Lock()
	ids := make([]int64, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	m.runningMu.Unlock()

	for _, id := range ids {
		item, err := m.store.GetByID(id)
		if err != nil || item == nil {
			continue
		}
		if !item.UserStopped() {
			continue
		}
		m.runningMu.Lock()
		cancels := make([]context.CancelFunc, 0, len(m.running[id]))
		for _, cancel := range m.running[id] {
			cancels = append(cancels, cancel)
		}
		m.runningMu.Unlock()
		for _, cancel := range cancels {
			m.pipeline.logger.Info("cancelling worker for stopped item",
				"decision_type", logs.DecisionStageExecution,
				"decision_result", "cancelled",
				"decision_reason", "item was stopped by user while its stage was running",
				"item_id", id,
			)
			cancel()
		}
	}
}

// Run executes the scheduler loop until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	m.runScheduler(ctx)
}

// runScheduler dispatches every ready task whose resource claims fit the
// budget, waking on task completion (plus a timer fallback for externally
// enqueued items). Items stay visibly pending until resources are free;
// no goroutine ever blocks invisibly holding queue state.
func (m *Manager) runScheduler(ctx context.Context) {
	p := m.pipeline
	runCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
	}()

	for {
		select {
		case err := <-m.persistenceFailures:
			p.logger.Error("workflow stopped after queue persistence failure",
				"event_type", "queue_persistence_critical",
				"error_hint", "queue state could not be persisted; workflow is stopping to avoid untracked side effects",
				"error", err,
			)
			cancel()
			return
		default:
		}

		m.cancelStoppedWorkers()
		m.dispatch(runCtx, &workers)

		select {
		case <-runCtx.Done():
			return
		case <-m.wake:
		case <-time.After(5 * time.Second):
		}
	}
}

// dispatch compiles task rows for eligible items and starts every ready
// task whose claims fit the current budget.
func (m *Manager) dispatch(ctx context.Context, workers *sync.WaitGroup) {
	p := m.pipeline

	items, err := m.store.List(p.stageOrder...)
	if err != nil {
		p.logger.Error("list items for scheduling failed",
			"event_type", "queue_fetch_error",
			"error_hint", "failed to list queue items",
			"error", err,
		)
		return
	}
	byID := make(map[int64]*queue.Item, len(items))
	for _, item := range items {
		if err := m.store.EnsureTasks(item, p.specs); err != nil {
			p.logger.Error("task compile failed",
				"event_type", "task_compile_error",
				"error_hint", "failed to compile task rows for item",
				"item_id", item.ID,
				"error", err,
			)
			continue
		}
		byID[item.ID] = item
	}

	ready, err := m.store.ReadyTasks()
	if err != nil {
		p.logger.Error("ready task query failed",
			"event_type", "queue_fetch_error",
			"error_hint", "failed to query ready tasks",
			"error", err,
		)
		return
	}

	// Drop blocked-wait state for tasks that left the ready set (stopped,
	// retried, or dispatched by an earlier pass).
	readyIDs := make(map[int64]struct{}, len(ready))
	for _, task := range ready {
		readyIDs[task.ID] = struct{}{}
	}
	m.blockedMu.Lock()
	for id := range m.blocked {
		if _, ok := readyIDs[id]; !ok {
			delete(m.blocked, id)
		}
	}
	m.blockedMu.Unlock()

	for _, task := range ready {
		if ctx.Err() != nil {
			return
		}
		item, ok := byID[task.ItemID]
		if !ok {
			continue
		}
		// Parallel branches of one item may run concurrently, but never
		// alongside a STALE worker: after a stop+retry recompiled the task
		// rows, an exiting cancelled worker still references a deleted task
		// and may share files with the new one. Skip the item until it
		// drains.
		if m.hasStaleWorker(item.ID) {
			continue
		}
		idx, ok := p.stageMap[task.Type]
		if !ok {
			p.logger.Error("unknown task type",
				"event_type", "unknown_stage",
				"error_hint", "task type not in pipeline map",
				"item_id", task.ItemID,
				"stage", task.Type,
			)
			continue
		}
		ps := p.stages[idx]

		claims := ps.Claims
		if ps.ClaimsFunc != nil {
			claims = ps.ClaimsFunc(item)
		}
		holder := httpapi.ResourceHolder{ItemID: item.ID, Task: string(task.Type)}
		if !m.reserve(claims, holder) {
			m.noteTaskBlocked(task, claims)
			continue
		}
		m.noteTaskGranted(task, claims)

		// Mark in_progress for the first worker of an overlap window;
		// sibling branches already hold the flag.
		if item.InProgress == 0 {
			if err := m.store.StartStage(item); err != nil {
				m.release(claims, holder)
				p.logger.Error("persist in_progress failed",
					"event_type", "progress_persist_failed",
					"error_hint", "failed to persist in_progress flag",
					"item_id", item.ID,
					"error", err,
				)
				continue
			}
		}
		if err := m.store.StartTask(task); err != nil {
			m.release(claims, holder)
			_ = m.store.ClearInProgress(item)
			p.logger.Error("persist task start failed",
				"event_type", "task_persist_failed",
				"error_hint", "failed to mark task running",
				"item_id", item.ID,
				"error", err,
			)
			continue
		}

		taskCtx, cancelTask := context.WithCancel(ctx)
		if !m.trackWorker(item.ID, task.ID, cancelTask) {
			// Lost a race with another dispatch pass; revert.
			cancelTask()
			m.release(claims, holder)
			_ = m.store.FinishTask(task, queue.TaskPending, "")
			_ = m.store.ClearInProgress(item)
			continue
		}

		// Each worker gets its OWN copy of the item: parallel branches of one
		// item dispatch from the same List() result, and workers mutate item
		// fields (Refresh, progress) concurrently.
		itemCopy := *item
		workers.Add(1)
		go func(task *queue.Task, item *queue.Item, ps PipelineStage, claims map[string]int, holder httpapi.ResourceHolder, taskCtx context.Context, cancelTask context.CancelFunc) {
			defer workers.Done()
			defer m.signalWake()
			defer m.release(claims, holder)
			defer cancelTask()
			m.runTask(taskCtx, task, item, ps, claims)
			m.untrackWorker(item.ID, task.ID)
			m.finalizeItem(item.ID)
		}(task, &itemCopy, ps, claims, holder, taskCtx, cancelTask)
	}
}

// noteTaskBlocked records and logs the first scheduler pass on which a ready
// task could not reserve its resource claims. Subsequent passes stay silent
// until the claim is granted.
func (m *Manager) noteTaskBlocked(task *queue.Task, claims map[string]int) {
	m.blockedMu.Lock()
	_, seen := m.blocked[task.ID]
	if !seen {
		m.blocked[task.ID] = time.Now()
	}
	m.blockedMu.Unlock()
	if seen {
		return
	}
	m.pipeline.logger.Info("task waiting for resources",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "blocked",
		"decision_reason", "resource claims exceed available budget",
		"item_id", task.ItemID,
		"stage", task.Type,
		"claims", logs.FormatCounts(claims),
	)
}

// noteTaskGranted logs the wait duration for a task that was previously
// blocked on resources; tasks that reserved on their first pass stay silent.
func (m *Manager) noteTaskGranted(task *queue.Task, claims map[string]int) {
	m.blockedMu.Lock()
	since, ok := m.blocked[task.ID]
	if ok {
		delete(m.blocked, task.ID)
	}
	m.blockedMu.Unlock()
	if !ok {
		return
	}
	m.pipeline.logger.Info("task resources granted",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "unblocked",
		"decision_reason", "resource claims now fit budget",
		"item_id", task.ItemID,
		"stage", task.Type,
		"claims", logs.FormatCounts(claims),
		"waited", time.Since(since).Round(time.Millisecond).String(),
	)
}

// runTask executes one task via the stage handler and records its terminal
// state. Cancellation, user stops, and persistence failures revert the task
// to pending: the item-level filters decide whether it ever runs again.
func (m *Manager) runTask(ctx context.Context, task *queue.Task, item *queue.Item, ps PipelineStage, claims map[string]int) {
	outcome := m.processItem(ctx, task, item, ps, claims)

	var state queue.TaskState
	var errMsg string
	switch outcome {
	case outcomeDone:
		state = queue.TaskDone
	case outcomeFailed:
		state = queue.TaskFailed
		if refreshed, err := m.store.GetByID(item.ID); err == nil && refreshed != nil {
			errMsg = refreshed.ErrorMessage
		}
	default:
		state = queue.TaskPending
	}
	if err := m.store.FinishTask(task, state, errMsg); err != nil {
		m.reportPersistenceFailure(m.pipeline.logger.With("item_id", item.ID), err,
			"task_persist_failed", "failed to record task state", item.ID)
	}
}

// itemOutcome classifies a stage execution for task bookkeeping.
type itemOutcome int

const (
	outcomeDone itemOutcome = iota
	outcomeFailed
	outcomeCanceled
	outcomeStopped
	outcomePersistence
)

// processItem executes a single stage handler for an item and reports the
// outcome. Item-level persistence (advance, failure, review, notifications)
// happens here; task-level bookkeeping is the scheduler caller's job.
func (m *Manager) processItem(ctx context.Context, task *queue.Task, item *queue.Item, ps PipelineStage, claims map[string]int) itemOutcome {
	p := m.pipeline

	itemLogger := p.logger.With("item_id", item.ID)

	itemLogger.Info("stage started",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "started",
		"decision_reason", fmt.Sprintf("item %d ready for %s", item.ID, ps.Stage),
		"stage", ps.Stage,
		"claims", logs.FormatCounts(claims),
	)
	m.maybeStartQueueCycle(ctx, itemLogger)

	res, err := stage.ExecuteWorkflowStage(ctx, item, stage.WorkflowOptions{
		Store:     m.store,
		Handler:   ps.Handler,
		Logger:    p.logger,
		Stage:     ps.Stage,
		NoAdvance: true,
		Task:      task,
	})
	if res.Canceled {
		if err != nil && !errors.Is(err, context.Canceled) {
			itemLogger.Error("persist after cancellation failed",
				"event_type", "cancellation_persist_failed",
				"error_hint", "failed to persist after cancellation",
				"error", err,
			)
		}
		return outcomeCanceled
	}
	if res.UserStopped {
		itemLogger.Info("stage result ignored after user stop",
			"decision_type", logs.DecisionStageExecution,
			"decision_result", "user_stopped",
			"decision_reason", "item was explicitly stopped before stage finalization",
			"stage", ps.Stage,
			"stage_duration", logs.FormatDuration(res.Duration),
		)
		m.maybeCompleteQueueCycle(ctx, itemLogger)
		return outcomeStopped
	}
	if err != nil {
		var persistenceErr *stage.PersistenceError
		if errors.As(err, &persistenceErr) {
			if m.statusTracker != nil {
				m.statusTracker.RecordFailure("queue persistence failed: " + persistenceErr.Err.Error())
			}
			eventType := "completion_persist_failed"
			hint := "failed to persist after stage completion"
			if res.Failed {
				eventType = "failure_persist_failed"
				hint = "failed to persist after stage failure"
			}
			m.reportPersistenceFailure(itemLogger, persistenceErr.Err, eventType, hint, item.ID)
			return outcomePersistence
		}
		m.recordStageFailure(ctx, item, err, ps, res.Duration)
		return outcomeFailed
	}
	if res.Degraded {
		itemLogger.Warn("stage completed with degraded behavior",
			"event_type", "stage_degraded",
			"error_hint", res.DegradedMsg,
			"impact", "continuing to next stage",
			"stage", ps.Stage,
			"stage_duration", logs.FormatDuration(res.Duration),
		)
	}

	itemLogger.Info("stage completed",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "completed",
		"event_type", "stage_complete",
		"stage", ps.Stage,
		"stage_duration", logs.FormatDuration(res.Duration),
	)

	if m.statusTracker != nil {
		m.statusTracker.RecordSuccess()
	}

	m.maybeCompleteQueueCycle(ctx, itemLogger)
	return outcomeDone
}

// finalizeItem derives and persists the item's display stage once no
// workers remain: the earliest not-done task (in registration order) is the
// item's stage, or completed when every task is done. With DAG templates a
// completing stage cannot know the next one, so advancement lives here
// rather than in the executor.
func (m *Manager) finalizeItem(itemID int64) {
	if m.hasLiveWorker(itemID) {
		// Sibling workers still running: the item's coarse stage stays put
		// until the overlap window drains. Observers read task rows, and
		// the disc-detection gate reads running drive tasks, so nothing
		// depends on refreshing the label mid-flight.
		return
	}
	p := m.pipeline
	item, err := m.store.GetByID(itemID)
	if err != nil || item == nil {
		if err != nil {
			p.logger.Error("finalize item load failed",
				"event_type", "queue_fetch_error",
				"error_hint", "failed to load item for stage derivation",
				"item_id", itemID,
				"error", err,
			)
		}
		return
	}
	if item.Stage == queue.StageFailed || item.Stage == queue.StageCompleted || item.UserStopped() {
		return
	}
	tasks, err := m.store.TasksForItem(itemID)
	if err != nil {
		p.logger.Error("finalize item tasks failed",
			"event_type", "queue_fetch_error",
			"error_hint", "failed to load tasks for stage derivation",
			"item_id", itemID,
			"error", err,
		)
		return
	}
	if len(tasks) == 0 {
		return
	}
	derived := queue.StageCompleted
	for _, t := range tasks {
		if t.State != queue.TaskDone {
			derived = t.Type
			break
		}
	}
	if derived == item.Stage {
		return
	}
	if err := m.store.CompleteStage(item, derived, true); err != nil {
		m.reportPersistenceFailure(p.logger.With("item_id", itemID), err,
			"completion_persist_failed", "failed to persist derived stage", itemID)
		return
	}
	p.logger.Debug("item stage derived",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "advanced",
		"decision_reason", fmt.Sprintf("earliest incomplete task is %s", derived),
		"item_id", itemID,
		"stage", derived,
	)
	if derived == queue.StageCompleted {
		m.logItemCompleted(item, tasks)
	}
}

// logItemCompleted emits the one-line lifecycle summary for an item whose
// every task finished: per-stage wall times plus the end-to-end total, so a
// completed run can be judged from a single log line.
func (m *Manager) logItemCompleted(item *queue.Item, tasks []*queue.Task) {
	attrs := []any{
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "completed",
		"event_type", "item_complete",
		"item_id", item.ID,
		"title", item.DisplayTitle(),
		"needs_review", item.NeedsReview == 1,
	}
	if created, ok := item.CreatedTime(); ok {
		attrs = append(attrs, "total_wall_time", time.Since(created).Round(time.Second).String())
	}
	for _, t := range tasks {
		if d, ok := t.Duration(); ok {
			attrs = append(attrs, string(t.Type)+"_duration", d.Round(time.Second).String())
		}
	}
	m.pipeline.logger.Info("item completed", attrs...)
}

// recordStageFailure records status/notification state after stage execution
// has persisted the failed queue state.
func (m *Manager) recordStageFailure(ctx context.Context, item *queue.Item, err error, ps PipelineStage, duration time.Duration) {
	p := m.pipeline
	itemLogger := p.logger.With("item_id", item.ID)

	itemLogger.Error("stage failed",
		"event_type", "stage_failure",
		"error_hint", ps.Stage,
		"error", err,
		"stage", ps.Stage,
		"stage_duration", logs.FormatDuration(duration),
	)

	if m.statusTracker != nil {
		m.statusTracker.RecordFailure(err.Error())
	}

	title := fmt.Sprintf("Failed: %s during %s", item.DisplayTitle(), queue.HumanStage(ps.Stage))
	msg := fmt.Sprintf("Processing stopped.\nStage: %s\nReason: %s\nItem ID: %d", queue.HumanStage(ps.Stage), err.Error(), item.ID)
	_ = notify.SendLogged(ctx, m.notifier, itemLogger, notify.EventError, title, msg,
		"stage", ps.Stage,
	)

	m.maybeCompleteQueueCycle(ctx, itemLogger)
}

func (m *Manager) reportPersistenceFailure(logger *slog.Logger, err error, eventType, hint string, itemID int64) {
	m.persistenceFailureOnce.Do(func() {
		logger.Error("queue persistence failed; workflow will stop",
			"event_type", eventType,
			"error_hint", hint,
			"impact", "workflow stopping to avoid untracked side effects",
			"error", err,
		)

		select {
		case m.persistenceFailures <- err:
		default:
		}

		_ = notify.SendLogged(context.Background(), m.notifier, logger, notify.EventError,
			"Workflow paused: queue persistence failed",
			fmt.Sprintf("Spindle could not persist queue state and stopped workflow processing to avoid untracked side effects.\nItem ID: %d\nReason: %s", itemID, err.Error()),
			"source_event_type", eventType,
		)
	})
}

// maybeStartQueueCycle sends queue_started once per backlog cycle.
func (m *Manager) maybeStartQueueCycle(ctx context.Context, logger *slog.Logger) {
	if m.notifier == nil || m.store == nil {
		return
	}

	activeCount, err := m.store.ActiveItemCount()
	if err != nil {
		logger.Error("check queue start failed",
			"event_type", "queue_check_failed",
			"error_hint", "failed to count active queue items",
			"error", err,
		)
		return
	}
	if activeCount < 2 {
		return
	}

	m.queueNotifyMu.Lock()
	defer m.queueNotifyMu.Unlock()
	if m.queueCycleActive {
		return
	}

	msg := fmt.Sprintf("Queue is active with %d items in progress or waiting.", activeCount)
	if err := notify.SendLogged(ctx, m.notifier, logger, notify.EventQueueStarted, "Queue started", msg,
		"active_count", activeCount,
	); err != nil {
		return
	}
	m.queueCycleActive = true
}

// maybeCompleteQueueCycle sends queue_completed when a started backlog cycle drains.
func (m *Manager) maybeCompleteQueueCycle(ctx context.Context, logger *slog.Logger) {
	if m.notifier == nil || m.store == nil {
		return
	}

	activeCount, err := m.store.ActiveItemCount()
	if err != nil {
		logger.Error("check queue completion failed",
			"event_type", "queue_check_failed",
			"error_hint", "failed to count active queue items",
			"error", err,
		)
		return
	}
	if activeCount > 0 {
		return
	}

	m.queueNotifyMu.Lock()
	defer m.queueNotifyMu.Unlock()
	if !m.queueCycleActive {
		logger.Info("queue completion notification suppressed",
			"event_type", "notification_suppressed",
			"notification_event", string(notify.EventQueueCompleted),
			"decision_reason", "no queue_started notification was sent for this cycle",
		)
		return
	}

	if err := notify.SendLogged(ctx, m.notifier, logger, notify.EventQueueCompleted, "Queue completed", "All queued items finished processing."); err != nil {
		return
	}
	m.queueCycleActive = false
}
