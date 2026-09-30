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
	var taskID int64
	var attempt int
	if opts.Task != nil {
		taskID, attempt = opts.Task.ID, opts.Task.Attempts
	}
	// A run has one start and one terminal event, including cancelled and
	// one-shot runs. The queue journal, not the diagnostic log, owns them.
	if eventErr := opts.Store.RecordEvent(queue.Event{ItemID: item.ID, TaskID: taskID, Attempt: attempt, Type: "stage_start", Stage: stageName}); eventErr != nil {
		return res, &PersistenceError{Op: "persist stage start", Err: eventErr}
	}
	var sess *Session
	defer func() {
		sess.endOpenActivities()
		kind := "stage_complete"
		switch {
		case res.Canceled:
			kind = "stage_canceled"
		case res.UserStopped:
			kind = "stage_stopped"
		case res.Degraded:
			kind = "stage_degraded"
		case err != nil || res.Failed:
			kind = "stage_failed"
		}
		eventErr := opts.Store.RecordEvent(queue.Event{
			ItemID: item.ID, TaskID: taskID, Attempt: attempt, Type: kind, Stage: stageName,
			DurationSeconds: time.Since(start).Seconds(),
		})
		if eventErr != nil {
			err = errors.Join(err, &PersistenceError{Op: "persist stage outcome", Err: eventErr})
		}
	}()
	// Handler lines carry the attribution through the session logger; shared
	// clients with daemon-wide loggers inherit it through ctx.
	attribution := []any{"item_id", item.ID, "stage", stageName, "task_id", taskID, "attempt", attempt}
	ctx = logs.ContextWith(ctx, attribution...)
	runLogger := logger.With(attribution...)
	sess, err = NewSession(ctx, opts.Store, item, opts.Task)
	if err == nil {
		sess.Logger = runLogger
		err = opts.Handler.Run(ctx, sess)
	}
	// A cancelled stage context makes the run a cancellation even when the
	// handler swallowed the interruption and returned success: a stage that
	// was cut short must revert to pending, never report completion.
	if err == nil && errors.Is(ctx.Err(), context.Canceled) {
		err = context.Canceled
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
