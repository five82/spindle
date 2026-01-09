package logging

import (
	"context"
	"log/slog"
)

type contextKey string

const (
	itemIDKey    contextKey = "item_id"
	stageKey     contextKey = "stage"
	laneKey      contextKey = "lane"
	requestIDKey contextKey = "request_id"
)

const (
	// FieldComponent is the standardized structured logging key for component names.
	FieldComponent = "component"
	// FieldItemID is the standardized structured logging key for queue item identifiers.
	FieldItemID = "item_id"
	// FieldStage is the standardized structured logging key for workflow stage names.
	FieldStage = "stage"
	// FieldLane is the standardized structured logging key for workflow lane names.
	FieldLane = "lane"
	// FieldEpisodeKey is the standardized structured logging key for episode identifiers (e.g. s01e02).
	FieldEpisodeKey = "episode_key"
	// FieldEpisodeLabel is the standardized structured logging key for user-friendly episode labels (e.g. S01E02).
	FieldEpisodeLabel = "episode_label"
	// FieldEpisodeIndex is the standardized structured logging key for 1-based episode index within a batch.
	FieldEpisodeIndex = "episode_index"
	// FieldEpisodeCount is the standardized structured logging key for total episodes in a batch.
	FieldEpisodeCount = "episode_count"
	// FieldCorrelationID is the standardized structured logging key for request correlation identifiers.
	FieldCorrelationID = "correlation_id"
	// FieldAlert flags warnings or anomalies that should stand out in structured logs.
	FieldAlert = "alert"
	// FieldProgressStage is the standardized key for progress stage labels.
	FieldProgressStage = "progress_stage"
	// FieldProgressPercent is the standardized key for progress percent (0-100).
	FieldProgressPercent = "progress_percent"
	// FieldProgressMessage is the standardized key for progress messages.
	FieldProgressMessage = "progress_message"
	// FieldProgressETA is the standardized key for progress ETA.
	FieldProgressETA = "progress_eta"
	// FieldDecisionType categorizes decision logs for filtering.
	FieldDecisionType = "decision_type"
	// FieldEventType categorizes lifecycle events (stage_start, stage_complete, status, etc.).
	FieldEventType = "event_type"
	// FieldErrorKind captures the error taxonomy (validation/config/external/etc.).
	FieldErrorKind = "error_kind"
	// FieldErrorOperation captures the failing operation name.
	FieldErrorOperation = "error_operation"
	// FieldErrorDetailPath points to additional diagnostics for an error.
	FieldErrorDetailPath = "error_detail_path"
	// FieldErrorCode captures stable error codes.
	FieldErrorCode = "error_code"
	// FieldErrorHint provides a short hint for recovery.
	FieldErrorHint = "error_hint"
)

// WithItemID annotates context with the queue item identifier.
func WithItemID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, itemIDKey, id)
}

// ItemIDFromContext extracts the queue item identifier if present.
func ItemIDFromContext(ctx context.Context) (int64, bool) {
	v := ctx.Value(itemIDKey)
	if v == nil {
		return 0, false
	}
	switch val := v.(type) {
	case int64:
		return val, true
	case int:
		return int64(val), true
	default:
		return 0, false
	}
}

// WithStage annotates context with the workflow stage name.
func WithStage(ctx context.Context, stage string) context.Context {
	if stage == "" {
		return ctx
	}
	return context.WithValue(ctx, stageKey, stage)
}

// StageFromContext returns the stage name if present.
func StageFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(stageKey)
	if str, ok := v.(string); ok && str != "" {
		return str, true
	}
	return "", false
}

// WithLane annotates context with the workflow lane name (foreground/background).
func WithLane(ctx context.Context, lane string) context.Context {
	if lane == "" {
		return ctx
	}
	return context.WithValue(ctx, laneKey, lane)
}

// LaneFromContext returns the lane name if present.
func LaneFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(laneKey)
	if str, ok := v.(string); ok && str != "" {
		return str, true
	}
	return "", false
}

// WithRequestID annotates context with a correlation identifier.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext extracts the correlation identifier if present.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	if v, ok := ctx.Value(requestIDKey).(string); ok && v != "" {
		return v, true
	}
	return "", false
}

// ContextFields extracts standardized slog attributes from the provided context.
func ContextFields(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	fields := make([]slog.Attr, 0, 3)
	if id, ok := ItemIDFromContext(ctx); ok {
		fields = append(fields, slog.Int64(FieldItemID, id))
	}
	if stage, ok := StageFromContext(ctx); ok {
		fields = append(fields, slog.String(FieldStage, stage))
	}
	if lane, ok := LaneFromContext(ctx); ok {
		fields = append(fields, slog.String(FieldLane, lane))
	}
	if rid, ok := RequestIDFromContext(ctx); ok {
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
