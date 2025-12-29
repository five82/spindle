package logging

import (
	"context"
	"log/slog"
)

// levelOverrideHandler enforces a per-logger minimum level while delegating
// output to the wrapped handler (which should be configured with the most
// verbose level needed globally).
type levelOverrideHandler struct {
	next  slog.Handler
	level slog.Level
}

func newLevelOverrideHandler(next slog.Handler, level slog.Level) slog.Handler {
	if next == nil {
		return NoopHandler{}
	}
	return &levelOverrideHandler{next: next, level: level}
}

func (h *levelOverrideHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level < h.level {
		return false
	}
	return h.next.Enabled(ctx, level)
}

func (h *levelOverrideHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level < h.level {
		return nil
	}
	return h.next.Handle(ctx, record)
}

func (h *levelOverrideHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelOverrideHandler{
		next:  h.next.WithAttrs(attrs),
		level: h.level,
	}
}

func (h *levelOverrideHandler) WithGroup(name string) slog.Handler {
	return &levelOverrideHandler{
		next:  h.next.WithGroup(name),
		level: h.level,
	}
}

func (h *levelOverrideHandler) CloneWithLevel(level slog.Level) slog.Handler {
	return &levelOverrideHandler{
		next:  h.next,
		level: level,
	}
}

// WithLevelOverride returns a logger that enforces the provided minimum level
// while preserving existing attributes and handler wiring.
func WithLevelOverride(logger *slog.Logger, level slog.Level) *slog.Logger {
	if logger == nil {
		return slog.New(newLevelOverrideHandler(nil, level))
	}
	if cloner, ok := logger.Handler().(interface{ CloneWithLevel(slog.Level) slog.Handler }); ok {
		return slog.New(cloner.CloneWithLevel(level))
	}
	return slog.New(newLevelOverrideHandler(logger.Handler(), level))
}
