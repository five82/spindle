package stageexec

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/five82/spindle/internal/logs"
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
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "started",
		"decision_reason", fmt.Sprintf("one-shot execution of %s", item.Stage),
		"stage", item.Stage,
		"disc_title", item.DiscTitle,
	)

	if err := opts.Store.StartStage(item, item.Stage); err != nil {
		return fmt.Errorf("set in_progress: %w", err)
	}

	_, err := stage.ExecuteStarted(ctx, item, stage.ExecuteOptions{
		Store:   opts.Store,
		Handler: opts.Handler,
		Logger:  opts.Logger,
		Stage:   item.Stage,
		Advance: false,
	})
	if err != nil {
		return fmt.Errorf("stage %s: %w", item.Stage, err)
	}

	logger.Info("one-shot stage execution completed",
		"decision_type", logs.DecisionStageExecution,
		"decision_result", "completed",
		"decision_reason", string(item.Stage),
		"stage", item.Stage,
	)
	return nil
}
