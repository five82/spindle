package logging

import (
	"context"

	"go.uber.org/zap"

	"spindle/internal/services"
)

const (
	// FieldItemID is the standardized structured logging key for queue item identifiers.
	FieldItemID = "item_id"
	// FieldStage is the standardized structured logging key for workflow stage names.
	FieldStage = "stage"
	// FieldCorrelationID is the standardized structured logging key for request correlation identifiers.
	FieldCorrelationID = "correlation_id"
)

// ContextFields extracts standardized zap fields from the provided context.
func ContextFields(ctx context.Context) []zap.Field {
	if ctx == nil {
		return nil
	}
	fields := make([]zap.Field, 0, 3)
	if id, ok := services.ItemIDFromContext(ctx); ok {
		fields = append(fields, zap.Int64(FieldItemID, id))
	}
	if stage, ok := services.StageFromContext(ctx); ok {
		fields = append(fields, zap.String(FieldStage, stage))
	}
	if rid, ok := services.RequestIDFromContext(ctx); ok {
		fields = append(fields, zap.String(FieldCorrelationID, rid))
	}
	return fields
}

// WithContext returns a logger augmented with structured fields derived from the supplied context.
func WithContext(ctx context.Context, logger *zap.Logger) *zap.Logger {
	if logger == nil {
		logger = zap.NewNop()
	}
	fields := ContextFields(ctx)
	if len(fields) == 0 {
		return logger
	}
	return logger.With(fields...)
}
