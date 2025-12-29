package workflow

import (
	"context"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/stage"
)

// StatusSummary represents lightweight workflow diagnostics.
type StatusSummary struct {
	Running     bool
	LastError   string
	LastItem    *queue.Item
	QueueStats  map[queue.Status]int
	StageHealth map[string]stage.Health
}

// Status returns the latest workflow information.
func (m *Manager) Status(ctx context.Context) StatusSummary {
	m.mu.RLock()
	running := m.running
	lastErr := m.lastErr
	lastItem := m.lastItem
	stageSet := make([]pipelineStage, 0)
	for _, kind := range m.laneOrder {
		lane := m.lanes[kind]
		if lane == nil {
			continue
		}
		stageSet = append(stageSet, lane.stages...)
	}
	m.mu.RUnlock()

	stats, err := m.store.Stats(ctx)
	if err != nil {
		m.logger.Warn("failed to read queue stats", logging.Error(err))
	}

	health := make(map[string]stage.Health, len(stageSet))
	for _, stg := range stageSet {
		handler := stg.handler
		if handler == nil {
			continue
		}
		health[stg.name] = handler.HealthCheck(ctx)
	}

	summary := StatusSummary{Running: running, QueueStats: stats, StageHealth: health}
	if lastErr != nil {
		summary.LastError = lastErr.Error()
	}
	if lastItem != nil {
		copy := *lastItem
		summary.LastItem = &copy
	}
	return summary
}

func (m *Manager) setLastError(err error) {
	m.mu.Lock()
	m.lastErr = err
	m.mu.Unlock()
}

func (m *Manager) setLastItem(item *queue.Item) {
	m.mu.Lock()
	if item != nil {
		copy := *item
		m.lastItem = &copy
	} else {
		m.lastItem = nil
	}
	m.mu.Unlock()
}
