package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/five82/spindle/internal/notify"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/services"
	"github.com/five82/spindle/internal/stage"
)

// Semaphore identifies a resource semaphore.
type Semaphore int

const (
	SemNone     Semaphore = iota
	SemDisc                       // guards optical drive
	SemEncode                     // guards SVT-AV1 encoder
	SemWhisperX                   // guards WhisperX GPU
)

// PipelineStage describes a single stage in the pipeline.
type PipelineStage struct {
	Name      string
	Handler   stage.Handler
	Stage     queue.Stage
	Semaphore Semaphore
}

// pipelineState holds runtime state for the pipeline.
type pipelineState struct {
	stages     []PipelineStage
	stageOrder []queue.Stage
	stageMap   map[queue.Stage]int
	sems       [3]chan struct{} // disc, encode, whisperx (capacity 1 each)
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
	store    *queue.Store
	notifier *notify.Notifier
	pipeline *pipelineState
	observer StatusObserver
}

// New creates a workflow manager. observer may be nil.
func New(store *queue.Store, notifier *notify.Notifier, logger *slog.Logger, observer StatusObserver) *Manager {
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
				"item_id", item.ID,
				"stage", item.Stage,
			)
			continue
		}
		ps := p.stages[idx]

		// Mark in_progress synchronously before spawning goroutine to prevent
		// the poll loop from picking up the same item on the next iteration.
		item.InProgress = 1
		if err := m.store.Update(item); err != nil {
			p.logger.Error("persist in_progress failed",
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
		"decision_type", "stage_execution",
		"decision_result", "started",
		"decision_reason", fmt.Sprintf("item %d ready for %s", item.ID, ps.Name),
		"stage", ps.Name,
	)

	err := ps.Handler.Run(ctx, item)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			item.InProgress = 0
			if updateErr := m.store.Update(item); updateErr != nil {
				itemLogger.Error("persist after cancellation failed",
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
				"stage", ps.Name,
			)
			// Fall through to advance stage.
		} else {
			m.handleStageFailure(ctx, item, err, ps)
			return
		}
	}

	// Advance to next stage.
	item.Stage = m.nextStage(item.Stage)
	item.InProgress = 0

	itemLogger.Info("stage completed",
		"decision_type", "stage_execution",
		"decision_result", "completed",
		"decision_reason", fmt.Sprintf("advancing to %s", item.Stage),
		"stage", ps.Name,
	)

	if err := m.store.Update(item); err != nil {
		itemLogger.Error("persist after stage completion failed",
			"error", err,
		)
	}

	if m.observer != nil {
		m.observer.RecordSuccess(item)
	}
}

// handleStageFailure records failure state, notifies, and checks queue completion.
func (m *Manager) handleStageFailure(ctx context.Context, item *queue.Item, err error, ps PipelineStage) {
	p := m.pipeline
	itemLogger := p.logger.With("item_id", item.ID)

	item.Stage = queue.StageFailed
	item.InProgress = 0
	item.FailedAtStage = string(ps.Stage)
	item.ErrorMessage = err.Error()

	itemLogger.Error("stage failed",
		"event_type", "stage_failure",
		"error_hint", ps.Name,
		"error", err,
		"stage", ps.Name,
	)

	if updateErr := m.store.Update(item); updateErr != nil {
		itemLogger.Error("persist after failure failed",
			"error", updateErr,
		)
	}

	if m.observer != nil {
		m.observer.RecordFailure(item, err.Error())
	}

	if m.notifier != nil {
		title := fmt.Sprintf("Stage failed: %s", ps.Name)
		msg := fmt.Sprintf("Item %d failed at %s: %s", item.ID, ps.Name, err.Error())
		if notifyErr := m.notifier.Send(ctx, notify.EventError, title, msg); notifyErr != nil {
			itemLogger.Error("failure notification failed",
				"error", notifyErr,
			)
		}
	}

	m.checkQueueCompletion(ctx)
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

// checkQueueCompletion sends a queue_completed notification if no
// non-terminal items remain.
func (m *Manager) checkQueueCompletion(ctx context.Context) {
	if m.notifier == nil {
		return
	}

	active, err := m.store.HasActiveItems()
	if err != nil {
		m.pipeline.logger.Error("check queue completion failed",
			"error", err,
		)
		return
	}
	if active {
		return
	}

	if notifyErr := m.notifier.Send(ctx, notify.EventQueueCompleted, "Queue completed", "All items have finished processing."); notifyErr != nil {
		m.pipeline.logger.Error("queue completion notification failed",
			"error", notifyErr,
		)
	}
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
