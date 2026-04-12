package notify

import (
	"context"
	"log/slog"

	"github.com/five82/spindle/internal/logs"
)

// SendLogged sends a notification and records the outcome in the supplied logger.
// attrs may include item_id or any other context that should accompany the log.
func SendLogged(ctx context.Context, notifier *Notifier, logger *slog.Logger, event Event, title, message string, attrs ...any) error {
	if notifier == nil {
		return nil
	}
	logger = logs.Default(logger)

	if err := notifier.Send(ctx, event, title, message); err != nil {
		base := []any{
			"event_type", "notification_failed",
			"notification_event", string(event),
			"notification_title", title,
			"error_hint", "notification delivery failed",
			"error", err,
		}
		base = append(base, attrs...)
		logger.Error("notification failed", base...)
		return err
	}

	base := []any{
		"event_type", "notification_sent",
		"notification_event", string(event),
		"notification_title", title,
		"priority", priority(event),
	}
	if tagList := tags(event); tagList != "" {
		base = append(base, "tags", tagList)
	}
	base = append(base, attrs...)
	logger.Info("notification sent", base...)
	return nil
}
