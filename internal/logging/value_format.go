package logging

import (
	"fmt"
	"log/slog"
	"strconv"
)

func attrString(v slog.Value) string {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return err.Error()
		}
		return fmt.Sprint(v.Any())
	default:
		return formatValue(v)
	}
}

func formatValue(v slog.Value) string {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if needsQuotes(s) {
			return strconv.Quote(s)
		}
		return s
	case slog.KindBool:
		return strconv.FormatBool(v.Bool())
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(v.Float64(), 'f', -1, 64)
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return formatTimestamp(v.Time())
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			msg := err.Error()
			if needsQuotes(msg) {
				return strconv.Quote(msg)
			}
			return msg
		}
		s := fmt.Sprint(v.Any())
		if needsQuotes(s) {
			return strconv.Quote(s)
		}
		return s
	default:
		s := v.String()
		if needsQuotes(s) {
			return strconv.Quote(s)
		}
		return s
	}
}

func needsQuotes(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r <= ' ' || r == '=' || r == '"' {
			return true
		}
	}
	return false
}
