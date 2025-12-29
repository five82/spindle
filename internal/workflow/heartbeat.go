package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"spindle/internal/logging"
	"spindle/internal/queue"
)

// HeartbeatMonitor manages item heartbeats and stale item reclamation.
type HeartbeatMonitor struct {
	store             *queue.Store
	logger            *slog.Logger
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
}

// NewHeartbeatMonitor creates a new monitor.
func NewHeartbeatMonitor(store *queue.Store, logger *slog.Logger, interval, timeout time.Duration) *HeartbeatMonitor {
	return &HeartbeatMonitor{
		store:             store,
		logger:            logger,
		heartbeatInterval: interval,
		heartbeatTimeout:  timeout,
	}
}

// ReclaimStaleItems identifies items that have stopped sending heartbeats and resets them.
func (h *HeartbeatMonitor) ReclaimStaleItems(ctx context.Context, logger *slog.Logger, statuses []queue.Status) error {
	if h.heartbeatTimeout <= 0 {
		return nil
	}
	if len(statuses) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-h.heartbeatTimeout)
	reclaimed, err := h.store.ReclaimStaleProcessing(ctx, cutoff, statuses...)
	if err != nil {
		return err
	}
	if reclaimed > 0 {
		logger.Debug("reclaimed stale items", logging.Int64("count", reclaimed))
	}
	return nil
}

// StartLoop runs a heartbeat updater for a specific item until context cancellation.
func (h *HeartbeatMonitor) StartLoop(ctx context.Context, wg *sync.WaitGroup, itemID int64) {
	defer wg.Done()
	ticker := time.NewTicker(h.heartbeatInterval)
	defer ticker.Stop()

	logger := logging.WithContext(ctx, h.logger.With(logging.String("component", "workflow-heartbeat")))
	var lastSnapshot string

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.store.UpdateHeartbeat(ctx, itemID); err != nil {
				// Check if this is a context cancellation (normal shutdown)
				if errors.Is(err, context.Canceled) {
					logger.Debug("daemon shutting down, heartbeat update cancelled")
				} else {
					logger.Warn("heartbeat update failed", logging.Error(err))
				}
				continue
			}
			h.logStatusSnapshot(ctx, logger, itemID, &lastSnapshot)
		}
	}
}

func (h *HeartbeatMonitor) logStatusSnapshot(ctx context.Context, logger *slog.Logger, itemID int64, lastSnapshot *string) {
	if h == nil || h.store == nil || logger == nil {
		return
	}
	item, err := h.store.GetByID(ctx, itemID)
	if err != nil || item == nil {
		return
	}
	snapshot := fmt.Sprintf("%s|%s|%.2f|%s", item.Status, item.ProgressStage, item.ProgressPercent, item.ProgressMessage)
	if lastSnapshot != nil && *lastSnapshot == snapshot {
		return
	}
	if lastSnapshot != nil {
		*lastSnapshot = snapshot
	}
	logger.Debug("status snapshot",
		logging.String(logging.FieldEventType, "status_snapshot"),
		logging.String("status", string(item.Status)),
		logging.String(logging.FieldProgressStage, strings.TrimSpace(item.ProgressStage)),
		logging.Float64(logging.FieldProgressPercent, item.ProgressPercent),
		logging.String(logging.FieldProgressMessage, strings.TrimSpace(item.ProgressMessage)),
	)
}
