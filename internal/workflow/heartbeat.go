package workflow

import (
	"context"
	"errors"
	"log/slog"
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
		logger.Info("reclaimed stale items", logging.Int64("count", reclaimed))
	}
	return nil
}

// StartLoop runs a heartbeat updater for a specific item until context cancellation.
func (h *HeartbeatMonitor) StartLoop(ctx context.Context, wg *sync.WaitGroup, itemID int64) {
	defer wg.Done()
	ticker := time.NewTicker(h.heartbeatInterval)
	defer ticker.Stop()

	logger := logging.WithContext(ctx, h.logger.With(logging.String("component", "workflow-heartbeat")))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.store.UpdateHeartbeat(ctx, itemID); err != nil {
				// Check if this is a context cancellation (normal shutdown)
				if errors.Is(err, context.Canceled) {
					logger.Info("daemon shutting down, heartbeat update cancelled")
				} else {
					logger.Warn("heartbeat update failed", logging.Error(err))
				}
			}
		}
	}
}
