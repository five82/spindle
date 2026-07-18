package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
// Scheduled success leaves advancement to the task scheduler; failure marks
// the item failed, and cancellation clears in_progress. In OneShot mode every
// outcome only clears in_progress so the caller can route the temporary item.
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
	if opts.OneShot {
		if err := opts.Store.StartStage(item); err != nil {
			return res, fmt.Errorf("set in_progress: %w", err)
		}
	}

	sess, err := NewSession(ctx, opts.Store, item, opts.Task)
	if err == nil {
		sess.Logger = logger.With("item_id", item.ID)
		err = opts.Handler.Run(ctx, sess)
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			res.Canceled = true
			if updateErr := opts.Store.ClearInProgress(item); updateErr != nil {
				if opts.OneShot {
					logOneShotPersistenceFailure(logger, "clear in_progress after cancellation", updateErr)
				} else {
					return res, &PersistenceError{Op: "clear in_progress after cancellation", Err: updateErr}
				}
			}
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
				if updateErr := opts.Store.ClearInProgress(item); updateErr != nil {
					logOneShotPersistenceFailure(logger, "clear in_progress after stage error", updateErr)
				}
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

	if opts.OneShot {
		if updateErr := opts.Store.ClearInProgress(item); updateErr != nil {
			logOneShotPersistenceFailure(logger, "clear in_progress after stage completion", updateErr)
		}
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

func logOneShotPersistenceFailure(logger *slog.Logger, op string, err error) {
	logger.Error("stage persistence failed",
		"event_type", "stage_persistence_failed",
		"error_hint", op,
		"error", err,
	)
}
