package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"spindle/internal/logging"
	"spindle/internal/notifications"
	"spindle/internal/queue"
)

func (m *Manager) notifyStageError(ctx context.Context, stageName string, item *queue.Item, stageErr error) {
	if m.notifier == nil || stageErr == nil {
		return
	}
	logger := logging.WithContext(ctx, m.logger.With(logging.String("component", "workflow-manager")))
	contextLabel := fmt.Sprintf("%s (item #%d)", stageName, item.ID)
	if err := m.notifier.Publish(ctx, notifications.EventError, notifications.Payload{
		"error":   stageErr,
		"context": contextLabel,
	}); err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			logger.Debug("daemon shutting down, could not send error notification")
		} else {
			logger.Debug("stage error notification failed", logging.Error(err))
		}
	}
}

func (m *Manager) onItemStarted(ctx context.Context) {
	if m.notifier == nil {
		return
	}
	stats, err := m.store.Stats(ctx)
	if err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			m.logger.Debug("daemon shutting down, could not get queue stats for start notification")
		} else {
			m.logger.Warn("queue stats unavailable for start notification; notification skipped",
				logging.Error(err),
				logging.String(logging.FieldEventType, "queue_stats_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
				logging.String(logging.FieldImpact, "start notification will not be sent"),
			)
		}
		return
	}
	m.mu.Lock()
	if m.queueActive {
		m.mu.Unlock()
		return
	}
	m.queueActive = true
	m.queueStart = time.Now()
	m.mu.Unlock()

	count := countWorkItems(stats)
	if err := m.notifier.Publish(ctx, notifications.EventQueueStarted, notifications.Payload{"count": count}); err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			m.logger.Debug("daemon shutting down, could not send queue start notification")
		} else {
			m.logger.Debug("queue start notification failed", logging.Error(err))
		}
	}
}

func (m *Manager) checkQueueCompletion(ctx context.Context) {
	if m.notifier == nil {
		return
	}
	stats, err := m.store.Stats(ctx)
	if err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			m.logger.Debug("daemon shutting down, could not check queue completion")
		} else {
			m.logger.Warn("queue stats unavailable for completion notification; notification skipped",
				logging.Error(err),
				logging.String(logging.FieldEventType, "queue_stats_failed"),
				logging.String(logging.FieldErrorHint, "check queue database access"),
				logging.String(logging.FieldImpact, "completion notification will not be sent"),
			)
		}
		return
	}
	if active := countActiveItems(stats); active > 0 {
		return
	}

	m.mu.Lock()
	if !m.queueActive {
		m.mu.Unlock()
		return
	}
	start := m.queueStart
	m.queueActive = false
	m.queueStart = time.Time{}
	m.mu.Unlock()

	duration := time.Duration(0)
	if !start.IsZero() {
		duration = time.Since(start)
	}
	processed := stats[queue.StatusCompleted]
	failed := stats[queue.StatusFailed]
	if err := m.notifier.Publish(ctx, notifications.EventQueueCompleted, notifications.Payload{
		"processed": processed,
		"failed":    failed,
		"duration":  duration,
	}); err != nil {
		// Check if this is a context cancellation (normal shutdown)
		if errors.Is(err, context.Canceled) {
			m.logger.Debug("daemon shutting down, could not send queue completion notification")
		} else {
			m.logger.Debug("queue completion notification failed", logging.Error(err))
		}
	}
}

func countWorkItems(stats map[queue.Status]int) int {
	total := 0
	for status, count := range stats {
		if status == queue.StatusCompleted || status == queue.StatusFailed {
			continue
		}
		total += count
	}
	return total
}

func countActiveItems(stats map[queue.Status]int) int {
	activeStatuses := []queue.Status{
		queue.StatusPending,
		queue.StatusIdentifying,
		queue.StatusIdentified,
		queue.StatusRipping,
		queue.StatusRipped,
		queue.StatusEncoding,
		queue.StatusEncoded,
		queue.StatusOrganizing,
	}
	total := 0
	for _, status := range activeStatuses {
		total += stats[status]
	}
	return total
}
