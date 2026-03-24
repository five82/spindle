package stageexec

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/stage"
)

// Options configures one-shot stage execution.
type Options struct {
	Store   *queue.Store
	Handler stage.Handler
	Logger  *slog.Logger
}

// Run executes a single stage handler for an item.
func Run(ctx context.Context, item *queue.Item, opts Options) error {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	logger := opts.Logger.With("item_id", item.ID)
	ctx = stage.WithLogger(ctx, logger)

	logger.Info("one-shot stage execution started",
		"stage", item.Stage,
		"disc_title", item.DiscTitle,
	)

	item.InProgress = 1
	if err := opts.Store.Update(item); err != nil {
		return fmt.Errorf("set in_progress: %w", err)
	}

	err := opts.Handler.Run(ctx, item)

	item.InProgress = 0
	if updateErr := opts.Store.Update(item); updateErr != nil {
		logger.Error("failed to clear in_progress",
			"event_type", "stage_persistence_failed",
			"error_hint", "failed to clear in_progress flag after stage execution",
			"error", updateErr,
		)
	}

	if err != nil {
		return fmt.Errorf("stage %s: %w", item.Stage, err)
	}

	logger.Info("one-shot stage execution completed", "stage", item.Stage)
	return nil
}
