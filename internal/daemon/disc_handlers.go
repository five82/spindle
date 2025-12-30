package daemon

import (
	"context"
	"fmt"
	"strings"

	"log/slog"

	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
)

type queueProcessor interface {
	Process(ctx context.Context, info discInfo, fingerprint string, logger *slog.Logger) (bool, error)
}

type fingerprintErrorNotifier interface {
	FingerprintFailed(ctx context.Context, info discInfo, err error, logger *slog.Logger)
}

type queueStoreProcessor struct {
	store *queue.Store
}

func newQueueStoreProcessor(store *queue.Store) *queueStoreProcessor {
	if store == nil {
		return nil
	}
	return &queueStoreProcessor{store: store}
}

func (p *queueStoreProcessor) Process(ctx context.Context, info discInfo, fingerprint string, logger *slog.Logger) (bool, error) {
	if p == nil || p.store == nil {
		return false, fmt.Errorf("queue processor unavailable")
	}

	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return false, fmt.Errorf("disc fingerprint is required")
	}

	existing, err := p.store.FindByFingerprint(ctx, fingerprint)
	if err != nil {
		return false, fmt.Errorf("lookup existing disc: %w", err)
	}

	if existing != nil {
		return p.handleExisting(ctx, info, existing, fingerprint, logger)
	}

	return p.enqueueNew(ctx, info, fingerprint, logger)
}

func (p *queueStoreProcessor) handleExisting(ctx context.Context, info discInfo, existing *queue.Item, fingerprint string, logger *slog.Logger) (bool, error) {
	label := strings.TrimSpace(info.Label)
	updated := false

	if existing.DiscFingerprint != fingerprint {
		existing.DiscFingerprint = fingerprint
		updated = true
	}

	status := existing.Status
	if status == queue.StatusCompleted {
		if label != "" && shouldRefreshDiscTitle(existing.DiscTitle) && label != strings.TrimSpace(existing.DiscTitle) {
			existing.DiscTitle = label
			updated = true
		}
		if updated {
			if err := p.store.Update(ctx, existing); err != nil {
				if logger != nil {
					logger.Warn("failed to update completed item",
						logging.Error(err),
						logging.Int64(logging.FieldItemID, existing.ID),
						logging.String(logging.FieldEventType, "queue_update_failed"),
						logging.String("impact", "disc title refresh was not saved"),
						logging.String(logging.FieldErrorHint, "Check queue database availability and disk health"))
				}
			}
			if logger != nil {
				logger.Debug(
					"refreshed completed disc metadata",
					logging.Int64(logging.FieldItemID, existing.ID),
					logging.String("disc_title", strings.TrimSpace(existing.DiscTitle)),
				)
			}
		}
		if logger != nil {
			logger.Debug(
				"disc already completed",
				logging.Int64(logging.FieldItemID, existing.ID),
				logging.String("status", string(existing.Status)),
			)
		}
		return true, nil
	}

	if existing.IsInWorkflow() {
		if label != "" && shouldRefreshDiscTitle(existing.DiscTitle) && label != strings.TrimSpace(existing.DiscTitle) {
			existing.DiscTitle = label
			updated = true
		}
		if updated {
			if err := p.store.Update(ctx, existing); err != nil {
				if logger != nil {
					logger.Warn("failed to update in-flight item",
						logging.Error(err),
						logging.Int64(logging.FieldItemID, existing.ID),
						logging.String(logging.FieldEventType, "queue_update_failed"),
						logging.String("impact", "disc title refresh was not saved"),
						logging.String(logging.FieldErrorHint, "Check queue database availability and disk health"))
				}
			}
		}
		if logger != nil {
			logger.Debug(
				"disc already in workflow",
				logging.Int64(logging.FieldItemID, existing.ID),
				logging.String("status", string(existing.Status)),
				logging.String("progress_stage", strings.TrimSpace(existing.ProgressStage)),
			)
		}
		return true, nil
	}

	existing.Status = queue.StatusPending
	existing.ErrorMessage = ""
	existing.ProgressStage = "Awaiting identification"
	existing.ProgressPercent = 0
	existing.ProgressMessage = ""
	existing.NeedsReview = false
	existing.ReviewReason = ""
	existing.DiscFingerprint = fingerprint
	if label != "" {
		existing.DiscTitle = label
	}

	if err := p.store.Update(ctx, existing); err != nil {
		return false, fmt.Errorf("failed to reset existing item: %w", err)
	}

	if logger != nil {
		logger.Debug(
			"reset existing disc for processing",
			logging.Int64(logging.FieldItemID, existing.ID),
			logging.String("disc_title", strings.TrimSpace(existing.DiscTitle)),
		)
	}
	return true, nil
}

func shouldRefreshDiscTitle(current string) bool {
	trimmed := strings.TrimSpace(current)
	if trimmed == "" {
		return true
	}
	return trimmed == "Unknown Disc"
}

func (p *queueStoreProcessor) enqueueNew(ctx context.Context, info discInfo, fingerprint string, logger *slog.Logger) (bool, error) {
	title := strings.TrimSpace(info.Label)
	if title == "" {
		title = "Unknown Disc"
	}

	item, err := p.store.NewDisc(ctx, title, fingerprint)
	if err != nil {
		return false, fmt.Errorf("failed to enqueue disc: %w", err)
	}

	if logger != nil {
		logger.Debug(
			"queued new disc",
			logging.Int64(logging.FieldItemID, item.ID),
			logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
			logging.String("fingerprint", fingerprint),
		)
	}
	return true, nil
}

type notifierAdapter struct {
	service notifications.Service
}

func newNotifierAdapter(service notifications.Service) *notifierAdapter {
	if service == nil {
		return nil
	}
	return &notifierAdapter{service: service}
}

func (n *notifierAdapter) FingerprintFailed(ctx context.Context, info discInfo, err error, logger *slog.Logger) {
	if n == nil || n.service == nil {
		return
	}
	if notifyErr := n.service.Publish(ctx, notifications.EventError, notifications.Payload{
		"error":   err,
		"context": info.Label,
	}); notifyErr != nil {
		if logger != nil {
			logger.Warn("failed to send fingerprint error notification",
				logging.Error(notifyErr),
				logging.String(logging.FieldEventType, "notification_failed"),
				logging.String("impact", "disc fingerprint error notification was not delivered"),
				logging.String(logging.FieldErrorHint, "Check ntfy configuration and connectivity"))
		}
	}
}
