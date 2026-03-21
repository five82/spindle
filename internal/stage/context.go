package stage

import (
	"context"
	"log/slog"
)

type ctxKey struct{}

// WithLogger attaches a logger to the context.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// LoggerFromContext retrieves the logger from context.
// Returns slog.Default() if no logger is attached.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
