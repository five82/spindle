package stage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/services"
)

// ExecuteOptions configures execution of a handler after an item has been
// marked in progress.
type ExecuteOptions struct {
	Store              *queue.Store
	Handler            Handler
	Logger             *slog.Logger
	Stage              queue.Stage
	NextStage          queue.Stage
	Advance            bool
	MarkFailed         bool
	DegradedSucceeds   bool
	PersistenceIsFatal bool
}

// ExecuteResult describes the queue-visible outcome of a stage invocation.
type ExecuteResult struct {
	Duration    time.Duration
	Degraded    bool
	DegradedMsg string
	Canceled    bool
	Failed      bool
}

// PersistenceError reports a queue write failure during stage lifecycle
// finalization.
type PersistenceError struct {
	Op  string
	Err error
}

func (e *PersistenceError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Err) }
func (e *PersistenceError) Unwrap() error { return e.Err }

// MarkStarted initializes queue-visible active stage state and persists it.
func MarkStarted(store *queue.Store, item *queue.Item, stage queue.Stage) error {
	item.InProgress = 1
	item.ProgressStage = string(stage)
	item.ProgressPercent = 0
	item.ProgressMessage = ""
	item.ActiveEpisodeKey = ""
	item.ProgressBytesCopied = 0
	item.ProgressTotalBytes = 0
	if store == nil {
		return nil
	}
	return store.Update(item)
}

// ExecuteStarted runs a handler for an item already marked in progress, then
// persists success, failure, cancellation, or one-shot completion state.
func ExecuteStarted(ctx context.Context, item *queue.Item, opts ExecuteOptions) (res ExecuteResult, err error) {
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
	ctx = WithLogger(ctx, logger.With("item_id", item.ID))

	sess, err := NewSession(ctx, opts.Store, item)
	if err == nil {
		err = opts.Handler.Run(ctx, sess)
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			res.Canceled = true
			item.InProgress = 0
			if updateErr := updateItem(opts.Store, item); updateErr != nil {
				if persistErr := maybePersistenceError(opts, "clear in_progress after cancellation", updateErr); persistErr != nil {
					return res, persistErr
				}
			}
			return res, err
		}

		var degraded *services.ErrDegraded
		if errors.As(err, &degraded) && opts.DegradedSucceeds {
			res.Degraded = true
			res.DegradedMsg = degraded.Msg
		} else {
			res.Failed = opts.MarkFailed
			if opts.MarkFailed {
				item.Stage = queue.StageFailed
				item.InProgress = 0
				item.FailedAtStage = string(stageName)
				item.ErrorMessage = err.Error()
				if updateErr := updateItem(opts.Store, item); updateErr != nil {
					if persistErr := maybePersistenceError(opts, "persist stage failure", updateErr); persistErr != nil {
						return res, persistErr
					}
				}
			} else {
				item.InProgress = 0
				if updateErr := updateItem(opts.Store, item); updateErr != nil {
					if persistErr := maybePersistenceError(opts, "clear in_progress after stage error", updateErr); persistErr != nil {
						return res, persistErr
					}
				}
			}
			return res, err
		}
	}

	item.InProgress = 0
	if opts.Advance {
		item.Stage = opts.NextStage
		item.ActiveEpisodeKey = ""
		if item.Stage == queue.StageCompleted {
			item.ProgressStage = string(queue.StageCompleted)
			item.ProgressPercent = 100
			item.ProgressMessage = "Completed"
		} else {
			item.ProgressStage = ""
			item.ProgressPercent = 0
			item.ProgressMessage = ""
		}
	}
	if updateErr := updateItem(opts.Store, item); updateErr != nil {
		if persistErr := maybePersistenceError(opts, "persist stage completion", updateErr); persistErr != nil {
			return res, persistErr
		}
	}
	return res, nil
}

func updateItem(store *queue.Store, item *queue.Item) error {
	if store == nil {
		return nil
	}
	return store.Update(item)
}

func maybePersistenceError(opts ExecuteOptions, op string, err error) error {
	if opts.PersistenceIsFatal {
		return &PersistenceError{Op: op, Err: err}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error("stage persistence failed",
		"event_type", "stage_persistence_failed",
		"error_hint", op,
		"error", err,
	)
	return nil
}
