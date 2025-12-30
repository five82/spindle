package workflow

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"spindle/internal/logging"
	"spindle/internal/queue"
)

// Start begins background processing.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return errors.New("workflow already running")
	}
	lanes := make([]*laneState, 0, len(m.laneOrder))
	for _, kind := range m.laneOrder {
		lane := m.lanes[kind]
		if lane == nil || len(lane.statusOrder) == 0 {
			continue
		}
		lanes = append(lanes, lane)
	}
	if len(lanes) == 0 {
		m.mu.Unlock()
		return errors.New("workflow stages not configured")
	}

	runCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.running = true

	for _, lane := range lanes {
		lane.logger = m.laneLogger(lane)
	}
	m.wg.Add(len(lanes))
	m.mu.Unlock()

	for _, lane := range lanes {
		go m.runLane(runCtx, lane)
	}

	return nil
}

// Stop terminates background processing and waits for completion.
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	m.running = false
	m.cancel = nil
	m.mu.Unlock()

	cancel()
	m.wg.Wait()
}

func (m *Manager) runLane(ctx context.Context, lane *laneState) {
	defer m.wg.Done()
	if lane == nil {
		return
	}
	logger := lane.logger
	if logger == nil {
		logger = m.logger
	}
	if logger == nil {
		logger = logging.NewNop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if lane.runReclaimer {
			if err := m.heartbeat.ReclaimStaleItems(ctx, logger, lane.processingStatuses); err != nil {
				logger.Warn("reclaim stale processing failed; stuck items may remain",
					logging.Error(err),
					logging.String(logging.FieldEventType, "heartbeat_reclaim_failed"),
					logging.String(logging.FieldErrorHint, "check queue database access"),
				)
			}
		}

		item, err := m.nextItemForLane(ctx, lane)
		if err != nil {
			m.handleNextItemError(ctx, logger, err)
			continue
		}
		if item == nil {
			m.waitForItemOrShutdown(ctx)
			continue
		}

		if err := m.processItem(ctx, lane, logger, item); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}

func (m *Manager) nextItemForLane(ctx context.Context, lane *laneState) (*queue.Item, error) {
	if lane == nil || len(lane.statusOrder) == 0 {
		return nil, nil
	}
	return m.store.NextForStatuses(ctx, lane.statusOrder...)
}

func (m *Manager) handleNextItemError(ctx context.Context, logger *slog.Logger, err error) {
	m.setLastError(err)
	logger.Error("failed to fetch next queue item",
		logging.Error(err),
		logging.String(logging.FieldEventType, "queue_fetch_failed"),
		logging.String(logging.FieldErrorHint, "check queue database access"),
	)
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(m.cfg.Workflow.ErrorRetryInterval) * time.Second):
	}
}

func (m *Manager) waitForItemOrShutdown(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(m.pollInterval):
	}
}
