package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/five82/spindle/internal/queue"
)

// WorkflowOptions configures workflow execution of a handler after an item has
// been marked in progress.
type WorkflowOptions struct {
	Store     *queue.Store
	Handler   Handler
	Logger    *slog.Logger
	Stage     queue.Stage
	NextStage queue.Stage
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

// ExecuteWorkflowStage runs a handler for an item already marked in progress,
// then persists the workflow lifecycle outcome: success advances, degraded
// success advances with a warning result, failure marks the item failed,
// cancellation clears in_progress, and persistence failures are returned.
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

	sess, err := NewSession(ctx, opts.Store, item)
	if err == nil {
		sess.Logger = logger.With("item_id", item.ID)
		err = opts.Handler.Run(ctx, sess)
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			res.Canceled = true
			if updateErr := opts.Store.ClearInProgress(item); updateErr != nil {
				return res, &PersistenceError{Op: "clear in_progress after cancellation", Err: updateErr}
			}
			if item.UserStopped() {
				res.UserStopped = true
			}
			return res, err
		}

		var degraded *ErrDegraded
		if errors.As(err, &degraded) {
			res.Degraded = true
			res.DegradedMsg = degraded.Msg
		} else {
			res.Failed = true
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

	if updateErr := opts.Store.CompleteStage(item, opts.NextStage, true); updateErr != nil {
		return res, &PersistenceError{Op: "persist stage completion", Err: updateErr}
	}
	if item.UserStopped() {
		res.UserStopped = true
	}
	return res, nil
}
