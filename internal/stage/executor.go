package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/queue"
)

// WorkflowOptions configures a scheduled or standalone handler invocation.
// The scheduler owns task state and derives item stage after sibling tasks
// finish. OneShot starts and clears the item here without changing its stage.
type WorkflowOptions struct {
	Store   *queue.Store
	Handler Handler
	Logger  *slog.Logger
	Stage   queue.Stage
	OneShot bool
	// Task is the scheduler task this execution runs; the session reports
	// progress against its row. Nil (OneShot) means in-memory progress only.
	Task *queue.Task
}

// ExecuteResult describes the queue-visible outcome of a stage invocation.
type ExecuteResult struct {
	Duration    time.Duration
	Degraded    bool
	DegradedMsg string
	Canceled    bool
	Failed      bool
	UserStopped bool
}

// PersistenceError reports a queue write failure during stage lifecycle
// finalization.
type PersistenceError struct {
	Op  string
	Err error
}

func (e *PersistenceError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Err) }
func (e *PersistenceError) Unwrap() error { return e.Err }

// ExecuteWorkflowStage runs a handler and persists its item-level outcome.
// Scheduled success leaves advancement to the task scheduler and failure
// marks the item failed. In OneShot mode nothing is persisted so the caller
// can route the temporary item.
func ExecuteWorkflowStage(ctx context.Context, item *queue.Item, opts WorkflowOptions) (res ExecuteResult, err error) {
	stageName := opts.Stage
	if stageName == "" {
		stageName = item.Stage
	}
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if opts.Store == nil {
		return res, fmt.Errorf("stage execution: nil queue store")
	}
	// The executor owns the stage_start/stage_complete lifecycle events:
	// exactly one of each per run, for scheduled and OneShot execution alike.
	// Handlers must not emit them.
	runLogger := logger.With("item_id", item.ID)
	sess, err := NewSession(ctx, opts.Store, item, opts.Task)
	if err == nil {
		sess.Logger = runLogger
		runLogger.Debug("stage started",
			"event_type", "stage_start",
			"stage", stageName,
		)
		err = opts.Handler.Run(ctx, sess)
	}

	if err != nil {
		// A cancelled stage context makes any handler error a cancellation:
		// subprocess stages surface the kill as e.g. "signal: killed" rather
		// than context.Canceled, and classifying that as a stage failure
		// would mark the item failed for work the daemon itself interrupted
		// (stop, drain, user stop) instead of reverting the task to pending.
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			res.Canceled = true
			if item.UserStopped() {
				res.UserStopped = true
			}
			if opts.OneShot {
				return res, fmt.Errorf("stage %s: %w", stageName, err)
			}
			return res, err
		}

		var degraded *ErrDegraded
		if errors.As(err, &degraded) && !opts.OneShot {
			res.Degraded = true
			res.DegradedMsg = degraded.Msg
		} else {
			res.Failed = true
			if opts.OneShot {
				if item.UserStopped() {
					res.UserStopped = true
					res.Failed = false
					return res, nil
				}
				return res, fmt.Errorf("stage %s: %w", stageName, err)
			}
			if updateErr := opts.Store.FailStage(item, stageName, err.Error()); updateErr != nil {
				return res, &PersistenceError{Op: "persist stage failure", Err: updateErr}
			}
			if item.UserStopped() {
				res.UserStopped = true
				res.Failed = false
				return res, nil
			}
			return res, err
		}
	}

	runLogger.Debug("stage completed",
		"event_type", "stage_complete",
		"stage", stageName,
		"stage_duration", logs.FormatDuration(time.Since(start)),
	)

	if opts.OneShot {
		return res, nil
	}

	// A user stop can race the handler. Refresh before the scheduler records
	// task completion so the stop state wins over successful finalization.
	if refreshErr := opts.Store.Refresh(item); refreshErr != nil {
		return res, &PersistenceError{Op: "refresh after stage completion", Err: refreshErr}
	}
	if item.UserStopped() {
		res.UserStopped = true
	}
	return res, nil
}
