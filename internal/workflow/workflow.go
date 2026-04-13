package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/services"
	"github.com/five82/spindle/internal/stage"
)

// Semaphore identifies a resource semaphore.
type Semaphore int

const (
	SemNone          Semaphore = iota
	SemDisc                    // guards optical drive
	SemEncode                  // guards SVT-AV1 encoder
	SemTranscription           // guards shared transcription runtime
)

// PipelineStage describes a single stage in the pipeline.
// Stage identifies which queue stage this handler owns: the handler picks up
// items whose item.Stage equals this value, and on success advances the item
// to the next handler's Stage (or StageCompleted for the last handler).
type PipelineStage struct {
	Handler   stage.Handler
	Stage     queue.Stage
	Semaphore Semaphore
}

// pipelineState holds runtime state for the pipeline.
type pipelineState struct {
	stages     []PipelineStage
	stageOrder []queue.Stage
	stageMap   map[queue.Stage]int
	sems       [3]chan struct{} // disc, encode, transcription (capacity 1 each)
	logger     *slog.Logger
}

// StatusObserver receives notifications about item processing outcomes.
// Implementations must be goroutine-safe.
type StatusObserver interface {
	RecordSuccess(item *queue.Item)
	RecordFailure(item *queue.Item, errMsg string)
}

// Manager runs the pipeline poll loop.
type Manager struct {
	store            *queue.Store
	notifier         *notify.Notifier
	pipeline         *pipelineState
	observer         StatusObserver
	queueNotifyMu    sync.Mutex
	queueCycleActive bool
}

// New creates a workflow manager. observer may be nil.
func New(store *queue.Store, notifier *notify.Notifier, observer StatusObserver, logger *slog.Logger) *Manager {
	return &Manager{
		store:    store,
		notifier: notifier,
		observer: observer,
		pipeline: &pipelineState{
			logger: logger,
		},
	}
}

// ConfigureStages registers an ordered slice of stage handlers.
// It builds the stageMap, derives stageOrder (disc-semaphore stages first),
// and initializes semaphore channels (capacity 1 each).
func (m *Manager) ConfigureStages(stages []PipelineStage) {
	p := m.pipeline
	p.stages = stages
	p.stageMap = make(map[queue.Stage]int, len(stages))

	for i, s := range stages {
		p.stageMap[s.Stage] = i
	}

	// Derive stageOrder: disc-semaphore stages first, then the rest.
	var disc []queue.Stage
	var rest []queue.Stage
	for _, s := range stages {
		if s.Semaphore == SemDisc {
			disc = append(disc, s.Stage)
		} else {
			rest = append(rest, s.Stage)
		}
	}
	p.stageOrder = append(disc, rest...)

	// Initialize semaphore channels (capacity 1 each).
	for i := range p.sems {
		p.sems[i] = make(chan struct{}, 1)
	}
}

// Run executes the pipeline poll loop until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) {
	p := m.pipeline

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		item, err := m.store.NextForStatuses(p.stageOrder...)
		if err != nil {
			p.logger.Error("fetch next item failed",
				"event_type", "queue_fetch_error",
				"error_hint", "failed to fetch next queue item",
				"error", err,
			)
			if !sleep(ctx, 10*time.Second) {
				return
			}
			continue
		}

		if item == nil {
			if !sleep(ctx, 5*time.Second) {
				return
			}
			continue
		}

		idx, ok := p.stageMap[item.Stage]
		if !ok {
			p.logger.Error("unknown stage for item",
				"event_type", "unknown_stage",
				"error_hint", "stage not in pipeline map",
				"item_id", item.ID,
				"stage", item.Stage,
			)
			continue
		}
		ps := p.stages[idx]

		// Mark in_progress synchronously before spawning goroutine to prevent
		// the poll loop from picking up the same item on the next iteration.
		// Initialize progress state so external consumers (flyer) know which
		// stage is active and stale progress from the prior stage is cleared.
		item.InProgress = 1
		item.ProgressStage = string(ps.Stage)
		item.ProgressPercent = 0
		item.ProgressMessage = ""
		if err := m.store.Update(item); err != nil {
			p.logger.Error("persist in_progress failed",
				"event_type", "progress_persist_failed",
				"error_hint", "failed to persist in_progress flag",
				"item_id", item.ID,
				"error", err,
			)
			continue
		}

		go func() {
			if ps.Semaphore != SemNone {
				if !m.acquireSem(ctx, ps.Semaphore) {
					// Release the in_progress flag if we can't acquire the semaphore.
					item.InProgress = 0
					if err := m.store.Update(item); err != nil {
						p.logger.Error("release in_progress after sem cancel failed",
							"event_type", "progress_persist_failed",
							"error_hint", "failed to release in_progress after semaphore cancel",
							"item_id", item.ID,
							"error", err,
						)
					}
					return
				}
				defer m.releaseSem(ps.Semaphore)
			}

			m.processItem(ctx, item, ps)
		}()
	}
}

// processItem executes a single stage handler for an item.
func (m *Manager) processItem(ctx context.Context, item *queue.Item, ps PipelineStage) {
	p := m.pipeline

	// Create child context with per-item logger.
	itemLogger := p.logger.With("item_id", item.ID)
	ctx = stage.WithLogger(ctx, itemLogger)

	itemLogger.Info("stage started",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "started",
		"decision_reason", fmt.Sprintf("item %d ready for %s", item.ID, ps.Stage),
		"stage", ps.Stage,
	)
	m.maybeStartQueueCycle(ctx, itemLogger)

	start := time.Now()
	err := ps.Handler.Run(ctx, item)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			item.InProgress = 0
			if updateErr := m.store.Update(item); updateErr != nil {
				itemLogger.Error("persist after cancellation failed",
					"event_type", "cancellation_persist_failed",
					"error_hint", "failed to persist after cancellation",
					"error", updateErr,
				)
			}
			return
		}

		var degraded *services.ErrDegraded
		if errors.As(err, &degraded) {
			itemLogger.Warn("stage completed with degraded behavior",
				"event_type", "stage_degraded",
				"error_hint", degraded.Msg,
				"impact", "continuing to next stage",
				"stage", ps.Stage,
				"stage_duration", time.Since(start),
			)
			// Fall through to advance stage.
		} else {
			m.handleStageFailure(ctx, item, err, ps, start)
			return
		}
	}

	// Advance to next stage and finalize progress.
	item.Stage = m.nextStage(item.Stage)
	item.InProgress = 0
	item.ProgressStage = ""
	item.ProgressPercent = 0
	item.ProgressMessage = ""

	itemLogger.Info("stage completed",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "completed",
		"decision_reason", fmt.Sprintf("advancing to %s", item.Stage),
		"event_type", "stage_complete",
		"stage", ps.Stage,
		"stage_duration", time.Since(start),
	)

	if err := m.store.Update(item); err != nil {
		itemLogger.Error("persist after stage completion failed",
			"event_type", "completion_persist_failed",
			"error_hint", "failed to persist after stage completion",
			"error", err,
		)
	}

	if m.observer != nil {
		m.observer.RecordSuccess(item)
	}

	m.maybeCompleteQueueCycle(ctx, itemLogger)
}

// handleStageFailure records failure state, notifies, and checks queue completion.
func (m *Manager) handleStageFailure(ctx context.Context, item *queue.Item, err error, ps PipelineStage, start time.Time) {
	p := m.pipeline
	itemLogger := p.logger.With("item_id", item.ID)

	item.Stage = queue.StageFailed
	item.InProgress = 0
	item.FailedAtStage = string(ps.Stage)
	item.ErrorMessage = err.Error()

	itemLogger.Error("stage failed",
		"event_type", "stage_failure",
		"error_hint", ps.Stage,
		"error", err,
		"stage", ps.Stage,
		"stage_duration", time.Since(start),
	)

	if updateErr := m.store.Update(item); updateErr != nil {
		itemLogger.Error("persist after failure failed",
			"event_type", "failure_persist_failed",
			"error_hint", "failed to persist after stage failure",
			"error", updateErr,
		)
	}

	if m.observer != nil {
		m.observer.RecordFailure(item, err.Error())
	}

	title := fmt.Sprintf("Failed: %s during %s", item.DisplayTitle(), queue.HumanStage(ps.Stage))
	msg := fmt.Sprintf("Processing stopped.\nStage: %s\nReason: %s\nItem ID: %d", queue.HumanStage(ps.Stage), err.Error(), item.ID)
	_ = notify.SendLogged(ctx, m.notifier, itemLogger, notify.EventError, title, msg,
		"item_id", item.ID,
		"stage", ps.Stage,
	)

	m.maybeCompleteQueueCycle(ctx, itemLogger)
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

// acquireSem acquires a semaphore, respecting context cancellation.
// Returns false if the context was cancelled before acquisition.
func (m *Manager) acquireSem(ctx context.Context, sem Semaphore) bool {
	ch := m.pipeline.sems[sem-1] // SemDisc=1 -> index 0, etc.
	select {
	case ch <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// releaseSem releases a semaphore.
func (m *Manager) releaseSem(sem Semaphore) {
	<-m.pipeline.sems[sem-1]
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

// sleep waits for the given duration or until ctx is cancelled.
// Returns false if the context was cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
