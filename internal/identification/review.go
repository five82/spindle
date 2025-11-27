package identification

import (
	"context"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
	"spindle/internal/services"
)

func (i *Identifier) handleDuplicateFingerprint(ctx context.Context, item *queue.Item) error {
	logger := logging.WithContext(ctx, i.logger)
	found, err := i.store.FindByFingerprint(ctx, item.DiscFingerprint)
	if err != nil {
		return services.Wrap(services.ErrTransient, "identification", "lookup fingerprint", "Failed to query existing disc fingerprint", err)
	}
	if found != nil && found.ID != item.ID {
		logger.Info(
			"duplicate disc fingerprint detected",
			logging.Int64("existing_item_id", found.ID),
			logging.String("fingerprint", item.DiscFingerprint),
		)
		i.flagReview(ctx, item, "Duplicate disc fingerprint", true)
		item.ErrorMessage = "Duplicate disc fingerprint"
	}
	return nil
}

func (i *Identifier) scheduleReview(ctx context.Context, item *queue.Item, message string) {
	i.flagReview(ctx, item, message, false)
}

func (i *Identifier) flagReview(ctx context.Context, item *queue.Item, message string, immediate bool) {
	logger := logging.WithContext(ctx, i.logger).With(logging.Int64(logging.FieldItemID, item.ID))
	logger.Warn(
		"flagging queue item for review",
		logging.String("reason", message),
		logging.Bool("immediate", immediate),
		logging.Alert("review"),
	)
	item.NeedsReview = true
	item.ReviewReason = message
	if strings.TrimSpace(item.ProgressStage) == "" || item.ProgressStage == "Identifying" {
		item.ProgressStage = "Needs review"
	}
	item.ProgressPercent = 100
	item.ProgressMessage = message
	item.ErrorMessage = message
	if immediate {
		item.Status = queue.StatusReview
		if i.notifier != nil {
			label := strings.TrimSpace(item.DiscTitle)
			if label == "" {
				label = item.DiscFingerprint
			}
			if label == "" {
				label = "Unidentified Disc"
			}
			if err := i.notifier.Publish(ctx, notifications.EventUnidentifiedMedia, notifications.Payload{"label": label}); err != nil {
				logger.Warn("unidentified media notification failed", logging.Error(err))
			}
		}
	} else {
		switch item.Status {
		case queue.StatusReview:
			// leave untouched if already review
		case queue.StatusIdentifying, queue.StatusPending, "":
			item.Status = queue.StatusIdentified
		default:
			// preserve existing status so workflow manager can decide
		}
	}
}
