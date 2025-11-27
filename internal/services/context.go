package services

import "context"

type contextKey string

const (
	itemIDKey    contextKey = "item_id"
	stageKey     contextKey = "stage"
	laneKey      contextKey = "lane"
	requestIDKey contextKey = "request_id"
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
