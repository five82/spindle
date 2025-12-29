package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"spindle/internal/logging"
	"spindle/internal/queue"
)

func (m *Manager) processItem(ctx context.Context, lane *laneState, laneLogger *slog.Logger, item *queue.Item) error {
	stage, ok := lane.stageForStatus(item.Status)
	if !ok {
		if laneLogger == nil {
			laneLogger = m.logger
		}
		if laneLogger == nil {
			laneLogger = logging.NewNop()
		}
		laneLogger.Warn("no stage configured for status", logging.String("status", string(item.Status)))
		m.waitForItemOrShutdown(ctx)
		return nil
	}

	requestID := uuid.NewString()
	stageCtx := withStageContext(ctx, lane, stage.name, item, requestID)
	stageLogger := m.stageLoggerForLane(stageCtx, lane, laneLogger, item)
	if aware, ok := stage.handler.(loggerAware); ok {
		aware.SetLogger(stageLogger)
	}

	if err := m.transitionToProcessing(stageCtx, lane, stage.processingStatus, stage.name, item); err != nil {
		stageLogger.Error("failed to transition item to processing", logging.Error(err))
		m.setLastError(err)
		return err
	}

	return m.executeStage(stageCtx, lane, stageLogger, stage, item)
}

func (m *Manager) executeStage(ctx context.Context, lane *laneState, stageLogger *slog.Logger, stage pipelineStage, item *queue.Item) error {
	stageStart := time.Now()
	stageLogger.Info(
		"stage started",
		logging.String(logging.FieldEventType, "stage_start"),
		logging.String("processing_status", string(stage.processingStatus)),
		logging.String("disc_title", strings.TrimSpace(item.DiscTitle)),
		logging.String("source_file", strings.TrimSpace(item.SourcePath)),
	)
	if lane != nil && lane.kind == laneBackground && lane.logger != nil {
		logging.WithContext(ctx, lane.logger).Debug(
			"background stage started",
			logging.String(logging.FieldStage, stage.name),
			logging.Int64(logging.FieldItemID, item.ID),
			logging.String("log_file", strings.TrimSpace(item.BackgroundLogPath)),
		)
	}

	handler := stage.handler
	if handler == nil {
		stageLogger.Warn("missing stage handler", logging.String("stage", stage.name))
		item.Status = queue.StatusFailed
		item.ErrorMessage = fmt.Sprintf("stage %s missing handler", stage.name)
		if err := m.store.Update(ctx, item); err != nil {
			stageLogger.Error("failed to persist missing handler failure", logging.Error(err))
		}
		m.setLastError(errors.New("stage handler unavailable"))
		return errors.New("stage handler unavailable")
	}

	if err := handler.Prepare(ctx, item); err != nil {
		m.handleStageFailure(ctx, stage.name, item, err)
		m.setLastError(err)
		return err
	}
	if err := m.store.Update(ctx, item); err != nil {
		wrapped := fmt.Errorf("persist stage preparation: %w", err)
		stageLogger.Error("failed to persist stage preparation", logging.Error(wrapped))
		m.setLastError(wrapped)
		return wrapped
	}

	execErr := m.executeWithHeartbeat(ctx, handler, item)
	if execErr != nil {
		if errors.Is(execErr, context.Canceled) {
			stageLogger.Debug("stage interrupted by shutdown")
			return execErr
		}
		m.handleStageFailure(ctx, stage.name, item, execErr)
		m.setLastError(execErr)
		return execErr
	}

	if item.Status == stage.processingStatus || item.Status == "" {
		item.Status = stage.doneStatus
	}
	item.LastHeartbeat = nil
	if item.Status == queue.StatusCompleted {
		currentLabel := strings.TrimSpace(item.ProgressStage)
		if !item.NeedsReview && !strings.Contains(strings.ToLower(currentLabel), "review") {
			item.ProgressStage = deriveStageLabel(queue.StatusCompleted)
		}
		if item.ProgressPercent < 100 {
			item.ProgressPercent = 100
		}
		if strings.TrimSpace(item.ProgressMessage) == "" {
			item.ProgressMessage = deriveStageLabel(queue.StatusCompleted)
		}
	}
	if err := m.store.Update(ctx, item); err != nil {
		wrapped := fmt.Errorf("persist stage result: %w", err)
		stageLogger.Error("failed to persist stage result", logging.Error(wrapped))
		m.setLastError(wrapped)
		return wrapped
	}
	stageLogger.Info(
		"stage completed",
		logging.String(logging.FieldEventType, "stage_complete"),
		logging.String("next_status", string(item.Status)),
		logging.String("progress_stage", strings.TrimSpace(item.ProgressStage)),
		logging.String("progress_message", strings.TrimSpace(item.ProgressMessage)),
		logging.Duration("stage_duration", time.Since(stageStart)),
	)
	if lane != nil && lane.kind == laneBackground && lane.logger != nil {
		logging.WithContext(ctx, lane.logger).Debug(
			"background stage completed",
			logging.String(logging.FieldStage, stage.name),
			logging.Int64(logging.FieldItemID, item.ID),
			logging.Duration("stage_duration", time.Since(stageStart)),
		)
	}
	m.setLastItem(item)
	m.checkQueueCompletion(ctx)
	return nil
}

func (m *Manager) executeWithHeartbeat(ctx context.Context, handler StageHandler, item *queue.Item) error {
	hbCtx, hbCancel := context.WithCancel(ctx)
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go m.heartbeat.StartLoop(hbCtx, &hbWG, item.ID)

	execErr := handler.Execute(ctx, item)
	hbCancel()
	hbWG.Wait()
	return execErr
}

func (m *Manager) transitionToProcessing(ctx context.Context, lane *laneState, processing queue.Status, stageName string, item *queue.Item) error {
	if processing == "" {
		return errors.New("processing status must not be empty")
	}

	m.setItemProcessingState(item, processing)
	if err := m.store.Update(ctx, item); err != nil {
		return fmt.Errorf("persist processing transition: %w", err)
	}
	m.setLastItem(item)
	if lane == nil || lane.notificationsEnabled {
		m.onItemStarted(ctx)
	}
	return nil
}

func (m *Manager) setItemProcessingState(item *queue.Item, processing queue.Status) {
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
