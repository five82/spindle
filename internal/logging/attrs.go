package logging

import (
	"context"
	"log/slog"
	"time"
)

type Attr = slog.Attr

type Value = slog.Value

func Any(key string, value any) Attr { return slog.Any(key, value) }

func Bool(key string, value bool) Attr { return slog.Bool(key, value) }

func Duration(key string, value time.Duration) Attr { return slog.Duration(key, value) }

func Float64(key string, value float64) Attr { return slog.Float64(key, value) }

func Int(key string, value int) Attr { return slog.Int(key, value) }

func Int64(key string, value int64) Attr { return slog.Int64(key, value) }

func Uint64(key string, value uint64) Attr { return slog.Uint64(key, value) }

func String(key string, value string) Attr { return slog.String(key, value) }

func Alert(value string) Attr { return slog.String(FieldAlert, value) }

func Group(key string, attrs ...Attr) Attr {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	return slog.Group(key, args...)
}

func Error(err error) Attr {
	if err == nil {
		return slog.String("error", "<nil>")
	}
	return slog.Any("error", err)
}

func attrsToArgs(attrs []Attr) []any {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	return args
}

func Args(attrs ...Attr) []any {
	return attrsToArgs(attrs)
}

func NewNop() *slog.Logger {
	return slog.New(NoopHandler{})
}

// NoopHandler discards all log output.
type NoopHandler struct{}

func (NoopHandler) Enabled(context.Context, slog.Level) bool { return false }

func (NoopHandler) Handle(context.Context, slog.Record) error { return nil }

func (NoopHandler) WithAttrs([]slog.Attr) slog.Handler { return NoopHandler{} }

func (NoopHandler) WithGroup(string) slog.Handler { return NoopHandler{} }
