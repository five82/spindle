package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
)

func (m *Manager) handleStageFailure(ctx context.Context, stageName string, item *queue.Item, stageErr error) {
	base := m.logger
	if base == nil {
		base = logging.NewNop()
	}
	logger := m.stageLoggerForLane(ctx, nil, base, item).With(logging.String("component", "workflow-manager"))

	message := m.classifyStageFailure(stageName, stageErr)
	m.setItemFailureState(item, message)

	details := services.Details(stageErr)
	attrs := []logging.Attr{
		logging.String("resolved_status", string(queue.StatusFailed)),
		logging.String("processing_status", string(queue.StatusFailed)),
		logging.String("error_message", strings.TrimSpace(message)),
		logging.Alert("stage_failure"),
		logging.String(logging.FieldErrorKind, string(details.Kind)),
		logging.String(logging.FieldErrorOperation, details.Operation),
		logging.String(logging.FieldErrorDetailPath, details.DetailPath),
		logging.String(logging.FieldErrorCode, details.Code),
		logging.String(logging.FieldErrorHint, details.Hint),
	}
	if details.Cause != nil {
		attrs = append(attrs, logging.Error(details.Cause))
	} else {
		attrs = append(attrs, logging.Error(stageErr))
	}
	attrs = append(attrs, logging.String(logging.FieldEventType, "stage_failure"))
	logger.Error("stage failed", logging.Args(attrs...)...)

	if err := m.store.Update(ctx, item); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Debug("daemon shutting down, could not update stage failure")
		} else {
			logger.Error("failed to persist stage failure", logging.Error(err))
		}
	}

	m.setLastItem(item)
	m.notifyStageError(ctx, stageName, item, stageErr)
	m.checkQueueCompletion(ctx)
}

func (m *Manager) classifyStageFailure(stageName string, stageErr error) string {
	if stageErr == nil {
		return m.getStageFailureMessage(stageName, "failed without error detail")
	}

	details := services.Details(stageErr)
	message := strings.TrimSpace(details.Message)
	if message == "" {
		message = strings.TrimSpace(stageErr.Error())
	}
	if message == "" {
		message = m.getStageFailureMessage(stageName, "failed")
	}
	return message
}

func (m *Manager) getStageFailureMessage(stageName, defaultMsg string) string {
	if stageName != "" {
		return fmt.Sprintf("%s %s", stageName, defaultMsg)
	}
	return fmt.Sprintf("workflow %s", defaultMsg)
}

func (m *Manager) setItemFailureState(item *queue.Item, message string) {
	item.SetFailed(message)
}
