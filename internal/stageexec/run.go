package stageexec

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
	"spindle/internal/stage"
)

// Handler is the stage contract used by the execution helper.
type Handler interface {
	Prepare(context.Context, *queue.Item) error
	Execute(context.Context, *queue.Item) error
}

// Options controls stage execution and queue persistence behavior.
type Options struct {
	Logger     *slog.Logger
	Store      *queue.Store
	Notifier   notifications.Service
	Handler    Handler
	StageName  string
	Processing queue.Status
	Done       queue.Status
	Item       *queue.Item
}

// Run executes a stage and applies queue transition semantics used by one-shot workflows.
func Run(ctx context.Context, opts Options) error {
	if opts.Handler == nil {
		return fmt.Errorf("stage handler unavailable: %s", opts.StageName)
	}
	if opts.Store == nil {
		return fmt.Errorf("queue store is required")
	}
	if opts.Item == nil {
		return fmt.Errorf("queue item is required")
	}

	stageCtx := logging.WithStage(ctx, opts.StageName)
	stageLogger := logging.WithContext(stageCtx, opts.Logger)
	if aware, ok := opts.Handler.(stage.LoggerAware); ok {
		aware.SetLogger(stageLogger)
	}

	stageLogger.Info(
		"stage started",
		logging.String(logging.FieldEventType, "stage_start"),
		logging.String("processing_status", string(opts.Processing)),
		logging.String("disc_title", strings.TrimSpace(opts.Item.DiscTitle)),
		logging.String("source_file", strings.TrimSpace(opts.Item.SourcePath)),
	)

	setItemProcessingState(opts.Item, opts.Processing)
	if err := opts.Store.Update(stageCtx, opts.Item); err != nil {
		return fmt.Errorf("persist processing transition: %w", err)
	}

	if err := opts.Handler.Prepare(stageCtx, opts.Item); err != nil {
		return handleFailure(stageCtx, stageLogger, opts.Store, opts.Notifier, opts.StageName, opts.Item, err)
	}
	if err := opts.Store.Update(stageCtx, opts.Item); err != nil {
		return fmt.Errorf("persist stage preparation: %w", err)
	}

	if err := opts.Handler.Execute(stageCtx, opts.Item); err != nil {
		return handleFailure(stageCtx, stageLogger, opts.Store, opts.Notifier, opts.StageName, opts.Item, err)
	}

	if opts.Item.Status == opts.Processing || opts.Item.Status == "" {
		opts.Item.Status = opts.Done
	}
	opts.Item.LastHeartbeat = nil
	if err := opts.Store.Update(stageCtx, opts.Item); err != nil {
		return fmt.Errorf("persist stage result: %w", err)
	}

	stageLogger.Info(
		"stage completed",
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.String("next_status", string(opts.Item.Status)),
		logging.String("progress_stage", strings.TrimSpace(opts.Item.ProgressStage)),
		logging.String("progress_message", strings.TrimSpace(opts.Item.ProgressMessage)),
	)

	return nil
}

func handleFailure(ctx context.Context, logger *slog.Logger, store *queue.Store, notifier notifications.Service, stageName string, item *queue.Item, stageErr error) error {
	message := "stage failed"
	if stageErr != nil {
		details := services.Details(stageErr)
		message = strings.TrimSpace(details.Message)
		if message == "" {
			message = strings.TrimSpace(stageErr.Error())
		}
	}
	item.SetFailed(message)

	logger.Error(
		"stage failed",
		logging.String(logging.FieldEventType, "stage_failure"),
		logging.String("resolved_status", string(queue.StatusFailed)),
		logging.String("error_message", strings.TrimSpace(message)),
		logging.Error(stageErr),
	)
	if err := store.Update(ctx, item); err != nil {
		logger.Error("failed to persist stage failure", logging.Error(err))
	}

	if notifier != nil && stageErr != nil {
		contextLabel := fmt.Sprintf("%s (item #%d)", stageName, item.ID)
		if err := notifier.Publish(ctx, notifications.EventError, notifications.Payload{
			"error":   stageErr,
			"context": contextLabel,
		}); err != nil {
			logger.Debug("stage error notification failed", logging.Error(err))
		}
	}

	return stageErr
}

func setItemProcessingState(item *queue.Item, processing queue.Status) {
	now := time.Now().UTC()
	item.Status = processing
	if item.ProgressStage == "" {
		item.ProgressStage = deriveStageLabel(processing)
	}
	if item.ProgressMessage == "" {
		item.ProgressMessage = fmt.Sprintf("%s started", deriveStageLabel(processing))
	}
	item.ProgressPercent = 0
	item.ErrorMessage = ""
	item.LastHeartbeat = &now
}

func deriveStageLabel(status queue.Status) string {
	if status == "" {
		return ""
	}
	parts := strings.Fields(strings.ReplaceAll(string(status), "_", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
