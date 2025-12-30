package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"spindle/internal/logging"
	"spindle/internal/queue"
	"spindle/internal/services"
)

func (m *Manager) laneLogger(lane *laneState) *slog.Logger {
	if m.logger == nil {
		return logging.NewNop()
	}
	name := lane.name
	if name == "" {
		name = string(lane.kind)
	}
	return m.logger.With(
		logging.String("component", fmt.Sprintf("workflow-%s-runner", name)),
		logging.String("lane", name),
	)
}

func (m *Manager) stageLoggerForLane(ctx context.Context, lane *laneState, laneLogger *slog.Logger, item *queue.Item) *slog.Logger {
	base := laneLogger
	if base == nil {
		base = m.logger
	}
	if base == nil {
		base = logging.NewNop()
	}

	if item != nil {
		path, _, err := m.bgLogger.Ensure(item)
		if err != nil {
			base.Warn("item log unavailable", logging.Error(err))
		} else {
			bgHandler, logErr := m.bgLogger.CreateHandler(path)
			if logErr != nil {
				base.Warn("failed to create item log writer", logging.Error(logErr))
			} else {
				// Item processing should log ONLY to the item log, not the daemon log.
				// Ensure item_id is baked into the logger so all logs are properly tagged.
				base = slog.New(bgHandler).With(logging.Int64(logging.FieldItemID, item.ID))
			}
		}
	}

	logger := logging.WithContext(ctx, base)
	if m != nil && m.cfg != nil {
		if stage, ok := services.StageFromContext(ctx); ok {
			if override := stageOverrideLevel(m.cfg.Logging.StageOverrides, stage); override != "" {
				logger = logging.WithLevelOverride(logger, parseStageLevel(override))
			}
		}
	}
	return logger
}

func stageOverrideLevel(overrides map[string]string, stage string) string {
	if len(overrides) == 0 {
		return ""
	}
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage == "" {
		return ""
	}
	for key, value := range overrides {
		if strings.ToLower(strings.TrimSpace(key)) == stage {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseStageLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func withStageContext(ctx context.Context, lane *laneState, stageName string, item *queue.Item, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if item != nil {
		ctx = services.WithItemID(ctx, item.ID)
	}
	if stageName != "" {
		ctx = services.WithStage(ctx, stageName)
	}
	if lane != nil {
		laneLabel := strings.TrimSpace(lane.name)
		if laneLabel == "" {
			laneLabel = string(lane.kind)
		}
		ctx = services.WithLane(ctx, laneLabel)
	}
	if requestID != "" {
		ctx = services.WithRequestID(ctx, requestID)
	}
	return ctx
}

func deriveStageLabel(status queue.Status) string {
	if status == "" {
		return ""
	}
	parts := strings.Fields(strings.ReplaceAll(string(status), "_", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
