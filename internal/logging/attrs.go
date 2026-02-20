package logging

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
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

// NewComponentLogger creates a logger with a standardized component attribute.
// If logger is nil, a no-op logger is used as the base.
func NewComponentLogger(logger *slog.Logger, component string) *slog.Logger {
	if logger == nil {
		logger = NewNop()
	}
	return logger.With(String(FieldComponent, component))
}

// HasAttrKey returns true if any attribute in attrs has the given key.
func HasAttrKey(attrs []Attr, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

// FieldImpact is the standardized key for user-facing consequence of a warning.
const FieldImpact = "impact"

// WarnWithContext logs a warning with enforced event_type, error_hint, and impact fields.
// If any of these fields are missing from attrs, defaults are injected.
// This ensures all WARN logs follow the guidance: cause + impact + next step.
func WarnWithContext(logger *slog.Logger, msg, eventType string, attrs ...Attr) {
	if logger == nil {
		return
	}
	if !HasAttrKey(attrs, FieldEventType) {
		attrs = append(attrs, String(FieldEventType, eventType))
	}
	if !HasAttrKey(attrs, FieldErrorHint) {
		attrs = append(attrs, String(FieldErrorHint, "check logs for details"))
	}
	if !HasAttrKey(attrs, FieldImpact) {
		attrs = append(attrs, String(FieldImpact, "operation completed with warnings"))
	}
	logger.Warn(msg, Args(attrs...)...)
}

// ErrorWithContext logs an error with enforced event_type and error_hint fields.
// If any of these fields are missing from attrs, defaults are injected.
func ErrorWithContext(logger *slog.Logger, msg, eventType string, attrs ...Attr) {
	if logger == nil {
		return
	}
	if !HasAttrKey(attrs, FieldEventType) {
		attrs = append(attrs, String(FieldEventType, eventType))
	}
	if !HasAttrKey(attrs, FieldErrorHint) {
		attrs = append(attrs, String(FieldErrorHint, "check logs for details"))
	}
	logger.Error(msg, Args(attrs...)...)
}

// NoopHandler discards all log output.
type NoopHandler struct{}

func (NoopHandler) Enabled(context.Context, slog.Level) bool { return false }

func (NoopHandler) Handle(context.Context, slog.Record) error { return nil }

func (NoopHandler) WithAttrs([]slog.Attr) slog.Handler { return NoopHandler{} }

func (NoopHandler) WithGroup(string) slog.Handler { return NoopHandler{} }

// ParseDecisionID extracts an integer ID from a decision string formatted as "ID:description".
// Returns the parsed ID and true if successful, or 0 and false otherwise.
func ParseDecisionID(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	parts := strings.SplitN(trimmed, ":", 2)
	if len(parts) == 0 {
		return 0, false
	}
	id, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, false
	}
	return id, true
}

// DecisionAttrs builds consistent attributes for decision logging.
// All decision logs should use decision_type, decision_result, and decision_reason.
func DecisionAttrs(decisionType, result, reason string) []Attr {
	return []Attr{
		String(FieldDecisionType, decisionType),
		String("decision_result", result),
		String("decision_reason", reason),
	}
}

// DecisionAttrsWithOptions builds decision attributes including the options field.
func DecisionAttrsWithOptions(decisionType, result, reason, options string) []Attr {
	return []Attr{
		String(FieldDecisionType, decisionType),
		String("decision_result", result),
		String("decision_reason", reason),
		String("decision_options", options),
	}
}
