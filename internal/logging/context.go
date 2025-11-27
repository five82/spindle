package logging

import (
	"context"
	"log/slog"

	"spindle/internal/services"
)

const (
	// FieldItemID is the standardized structured logging key for queue item identifiers.
	FieldItemID = "item_id"
	// FieldStage is the standardized structured logging key for workflow stage names.
	FieldStage = "stage"
	// FieldCorrelationID is the standardized structured logging key for request correlation identifiers.
	FieldCorrelationID = "correlation_id"
	// FieldAlert flags warnings or anomalies that should stand out in structured logs.
	FieldAlert = "alert"
)

// ContextFields extracts standardized slog attributes from the provided context.
func ContextFields(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	fields := make([]slog.Attr, 0, 3)
	if id, ok := services.ItemIDFromContext(ctx); ok {
		fields = append(fields, slog.Int64(FieldItemID, id))
	}
	if stage, ok := services.StageFromContext(ctx); ok {
		fields = append(fields, slog.String(FieldStage, stage))
	}
	if rid, ok := services.RequestIDFromContext(ctx); ok {
		fields = append(fields, slog.String(FieldCorrelationID, rid))
	}
	return fields
}

// WithContext returns a logger augmented with structured fields derived from the supplied context.
func WithContext(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = NewNop()
	}
	fields := ContextFields(ctx)
	if len(fields) == 0 {
		return logger
	}
	return logger.With(attrsToArgs(fields)...)
}
