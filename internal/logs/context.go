package logs

import (
	"context"
	"log/slog"
)

type contextAttrsKey struct{}

// ContextWith returns ctx carrying slog key/value attributes that every record
// logged through a *Context method with that ctx inherits. The stage executor
// uses it to attribute log lines from shared clients (LLM, OpenSubtitles,
// TMDB, Loom, transcription), whose loggers are daemon-wide, to the item and
// stage whose work issued the call.
func ContextWith(ctx context.Context, args ...any) context.Context {
	var r slog.Record
	r.Add(args...)
	attrs, _ := ctx.Value(contextAttrsKey{}).([]slog.Attr)
	attrs = append([]slog.Attr(nil), attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	return context.WithValue(ctx, contextAttrsKey{}, attrs)
}

// contextHandler adds ContextWith attributes to each record. A key already
// bound through Logger.With or present on the record wins, so a session
// logger that carries the same attributes never emits them twice.
type contextHandler struct {
	inner slog.Handler
	bound map[string]bool
}

// NewContextHandler wraps inner so records inherit ContextWith attributes.
func NewContextHandler(inner slog.Handler) slog.Handler {
	return &contextHandler{inner: inner}
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	attrs, _ := ctx.Value(contextAttrsKey{}).([]slog.Attr)
	if len(attrs) > 0 {
		present := make(map[string]bool, len(h.bound)+r.NumAttrs())
		for k := range h.bound {
			present[k] = true
		}
		r.Attrs(func(a slog.Attr) bool {
			present[a.Key] = true
			return true
		})
		for _, a := range attrs {
			if !present[a.Key] {
				r.AddAttrs(a)
			}
		}
	}
	return h.inner.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := make(map[string]bool, len(h.bound)+len(attrs))
	for k := range h.bound {
		bound[k] = true
	}
	for _, a := range attrs {
		bound[a.Key] = true
	}
	return &contextHandler{inner: h.inner.WithAttrs(attrs), bound: bound}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{inner: h.inner.WithGroup(name), bound: h.bound}
}
