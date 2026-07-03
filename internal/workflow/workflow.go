package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	Claims  map[string]int
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

	wake       chan struct{}
	budgetMu   sync.Mutex
	budgetCap  map[string]int
	budgetUsed map[string]int

	// running tracks the cancel function of each item's active worker. It
	// guards against dispatching a second task for an item whose previous
	// worker has not exited (StopItems clears in_progress while the worker
	// is still alive), and lets the scheduler cancel workers of
	// user-stopped items.
	runningMu sync.Mutex
	running   map[int64]context.CancelFunc
}

// New creates a workflow manager. statusTracker may be nil.
func New(store *queue.Store, notifier *notify.Notifier, statusTracker *httpapi.StatusTracker, logger *slog.Logger) *Manager {
	return &Manager{
		store:               store,
		notifier:            notifier,
		statusTracker:       statusTracker,
		persistenceFailures: make(chan error, 1),
		wake:                make(chan struct{}, 1),
		running:             make(map[int64]context.CancelFunc),
		pipeline: &pipelineState{
			logger: logs.Default(logger),
		},
	}
}

// ConfigureStages registers an ordered slice of stage handlers.
func (m *Manager) ConfigureStages(stages []PipelineStage) {
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

	// Task specs mirror registration order; the compiled per-item chain is
	// one task per stage at the current granularity.
	p.specs = make([]queue.TaskSpec, len(stages))
	for i, s := range stages {
		p.specs[i] = queue.TaskSpec{Type: s.Stage}
	}

	// Budget capacities replicate today's exclusivity: every claimed
	// resource has capacity 1. Raising a capacity is a Phase 4/5 change
	// gated by the GPU coexistence validation (see the plan doc).
	m.budgetCap = make(map[string]int)
	m.budgetUsed = make(map[string]int)
	for _, s := range stages {
		for res := range s.Claims {
			m.budgetCap[res] = 1
		}
	}
}

// reserve attempts to claim the stage's resources. It is all-or-nothing.
func (m *Manager) reserve(claims map[string]int) bool {
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	for res, n := range claims {
		if m.budgetUsed[res]+n > m.budgetCap[res] {
			return false
		}
	}
	for res, n := range claims {
		m.budgetUsed[res] += n
	}
	return true
}

// release returns a stage's claimed resources to the budget.
func (m *Manager) release(claims map[string]int) {
	m.budgetMu.Lock()
	defer m.budgetMu.Unlock()
	for res, n := range claims {
		m.budgetUsed[res] -= n
	}
}

// signalWake nudges the scheduler loop without blocking.
func (m *Manager) signalWake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// trackWorker registers an item's active worker cancel function. Returns
// false when the item already has a live worker.
func (m *Manager) trackWorker(itemID int64, cancel context.CancelFunc) bool {
	m.runningMu.Lock()
	defer m.runningMu.Unlock()
	if _, live := m.running[itemID]; live {
		return false
	}
	m.running[itemID] = cancel
	return true
}

// untrackWorker removes an item's worker registration.
func (m *Manager) untrackWorker(itemID int64) {
	m.runningMu.Lock()
	defer m.runningMu.Unlock()
	delete(m.running, itemID)
}

// hasLiveWorker reports whether the item has a worker that has not exited.
func (m *Manager) hasLiveWorker(itemID int64) bool {
	m.runningMu.Lock()
	defer m.runningMu.Unlock()
	_, live := m.running[itemID]
	return live
}

// cancelStoppedWorkers cancels the workers of items the user has stopped.
// Without this, `queue stop` only flips queue state and the stage worker
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
		cancel, live := m.running[id]
		m.runningMu.Unlock()
		if live {
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

	for _, task := range ready {
		if ctx.Err() != nil {
			return
		}
		item, ok := byID[task.ItemID]
		if !ok {
			continue
		}
		// Never run two workers for one item, even when queue state says the
		// item is free: StopItems clears in_progress while the stopped
		// worker is still exiting.
		if m.hasLiveWorker(item.ID) {
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

		if !m.reserve(ps.Claims) {
			continue
		}

		// Mark in_progress synchronously so the next dispatch pass cannot
		// pick up another task for the same item.
		if err := m.store.StartStage(item, ps.Stage); err != nil {
			m.release(ps.Claims)
			p.logger.Error("persist in_progress failed",
				"event_type", "progress_persist_failed",
				"error_hint", "failed to persist in_progress flag",
				"item_id", item.ID,
				"error", err,
			)
			continue
		}
		if err := m.store.StartTask(task); err != nil {
			m.release(ps.Claims)
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
		if !m.trackWorker(item.ID, cancelTask) {
			// Lost a race with another dispatch pass; revert.
			cancelTask()
			m.release(ps.Claims)
			_ = m.store.FinishTask(task, queue.TaskPending, "")
			_ = m.store.ClearInProgress(item)
			continue
		}

		workers.Add(1)
		go func(task *queue.Task, item *queue.Item, ps PipelineStage, taskCtx context.Context, cancelTask context.CancelFunc) {
			defer workers.Done()
			defer m.signalWake()
			defer m.release(ps.Claims)
			defer m.untrackWorker(item.ID)
			defer cancelTask()
			m.runTask(taskCtx, task, item, ps)
		}(task, item, ps, taskCtx, cancelTask)
	}
}

// runTask executes one task via the stage handler and records its terminal
// state. Cancellation, user stops, and persistence failures revert the task
// to pending: the item-level filters decide whether it ever runs again.
func (m *Manager) runTask(ctx context.Context, task *queue.Task, item *queue.Item, ps PipelineStage) {
	outcome := m.processItem(ctx, item, ps)

	var state, errMsg string
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
		m.pipeline.logger.Error("persist task finish failed",
			"event_type", "task_persist_failed",
			"error_hint", "failed to record task state",
			"item_id", item.ID,
			"stage", task.Type,
			"error", err,
		)
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
func (m *Manager) processItem(ctx context.Context, item *queue.Item, ps PipelineStage) itemOutcome {
	p := m.pipeline

	itemLogger := p.logger.With("item_id", item.ID)

	itemLogger.Info("stage started",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "started",
		"decision_reason", fmt.Sprintf("item %d ready for %s", item.ID, ps.Stage),
		"stage", ps.Stage,
	)
	m.maybeStartQueueCycle(ctx, itemLogger)

	res, err := stage.ExecuteWorkflowStage(ctx, item, stage.WorkflowOptions{
		Store:     m.store,
		Handler:   ps.Handler,
		Logger:    p.logger,
		Stage:     ps.Stage,
		NextStage: m.nextStage(item.Stage),
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
			"stage_duration", res.Duration,
		)
		m.maybeCompleteQueueCycle(ctx, itemLogger)
		return outcomeStopped
	}
	if err != nil {
		var persistenceErr *stage.PersistenceError
		if errors.As(err, &persistenceErr) {
			if m.statusTracker != nil {
				m.statusTracker.RecordFailure(item, "queue persistence failed: "+persistenceErr.Err.Error())
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
			"stage_duration", res.Duration,
		)
	}

	itemLogger.Info("stage completed",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "completed",
		"decision_reason", fmt.Sprintf("advancing to %s", item.Stage),
		"event_type", "stage_complete",
		"stage", ps.Stage,
		"stage_duration", res.Duration,
	)

	if m.statusTracker != nil {
		m.statusTracker.RecordSuccess(item)
	}

	m.maybeCompleteQueueCycle(ctx, itemLogger)
	return outcomeDone
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
		"stage_duration", duration,
	)

	if m.statusTracker != nil {
		m.statusTracker.RecordFailure(item, err.Error())
	}

	title := fmt.Sprintf("Failed: %s during %s", item.DisplayTitle(), queue.HumanStage(ps.Stage))
	msg := fmt.Sprintf("Processing stopped.\nStage: %s\nReason: %s\nItem ID: %d", queue.HumanStage(ps.Stage), err.Error(), item.ID)
	_ = notify.SendLogged(ctx, m.notifier, itemLogger, notify.EventError, title, msg,
		"item_id", item.ID,
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
			"item_id", itemID,
			"source_event_type", eventType,
		)
	})
}

// nextStage returns the stage following current in the pipeline.
// Returns completed if current is the last stage.
func (m *Manager) nextStage(current queue.Stage) queue.Stage {
	p := m.pipeline
	idx, ok := p.stageMap[current]
	if !ok {
		return queue.StageCompleted
	}
	if idx+1 >= len(p.stages) {
		return queue.StageCompleted
	}
	return p.stages[idx+1].Stage
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
