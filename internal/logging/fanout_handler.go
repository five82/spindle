package logging

import (
	"context"
	"log/slog"
)

type fanoutHandler struct {
	handlers []slog.Handler
}

func newFanoutHandler(handlers ...slog.Handler) slog.Handler {
	filtered := handlers[:0]
	for _, h := range handlers {
		if h != nil {
			filtered = append(filtered, h)
		}
	}
	if len(filtered) == 0 {
		return NoopHandler{}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return &fanoutHandler{handlers: append([]slog.Handler(nil), filtered...)}
}

func (h *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var firstErr error
	for idx, handler := range h.handlers {
		// Only call Handle on handlers that accept this log level
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		rec := record
		if idx < len(h.handlers)-1 {
			rec = record.Clone()
		}
		if err := handler.Handle(ctx, rec); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		next[i] = handler.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: next}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		next[i] = handler.WithGroup(name)
	}
	return &fanoutHandler{handlers: next}
}

// TeeLogger duplicates log output from base into the provided handlers.
func TeeLogger(base *slog.Logger, handlers ...slog.Handler) *slog.Logger {
	if base == nil {
		return slog.New(newFanoutHandler(handlers...))
	}
	all := append([]slog.Handler{base.Handler()}, handlers...)
	return slog.New(newFanoutHandler(all...))
}

// TeeHandler creates a handler that duplicates log output to multiple handlers.
func TeeHandler(handlers ...slog.Handler) slog.Handler {
	return newFanoutHandler(handlers...)
}
