package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/five82/spindle/internal/queue"
)

// WorkflowOptions configures workflow execution of a handler. Normal workflow
// callers mark the item in progress before execution and advance on success.
// OneShot handles standalone CLI execution by starting the stage here and
// clearing in_progress without advancing or failing the queue item.
type WorkflowOptions struct {
	Store     *queue.Store
	Handler   Handler
	Logger    *slog.Logger
	Stage     queue.Stage
	NextStage queue.Stage
	OneShot   bool
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

// ExecuteWorkflowStage runs a handler and persists the stage lifecycle outcome:
// success advances, degraded success advances with a warning result, failure
// marks the item failed, cancellation clears in_progress, and persistence
// failures are returned. In OneShot mode, success and failure only clear
// in_progress so the caller can route the temporary item explicitly.
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
		if err := opts.Store.StartStage(item, stageName); err != nil {
			return res, fmt.Errorf("set in_progress: %w", err)
		}
	}

	sess, err := NewSession(ctx, opts.Store, item)
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

	advance := !opts.OneShot
	if updateErr := opts.Store.CompleteStage(item, opts.NextStage, advance); updateErr != nil {
		if opts.OneShot {
			logOneShotPersistenceFailure(logger, "persist stage completion", updateErr)
			return res, nil
		}
		return res, &PersistenceError{Op: "persist stage completion", Err: updateErr}
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
