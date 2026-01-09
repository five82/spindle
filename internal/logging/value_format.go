package logging

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"
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

// FormatBytes returns a human-readable byte size string.
func FormatBytes(bytes int64) string {
	if bytes < 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	value := float64(bytes) / float64(div)
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	// Use appropriate precision based on size
	if value >= 100 {
		return fmt.Sprintf("%.0f %s", value, units[exp])
	} else if value >= 10 {
		return fmt.Sprintf("%.1f %s", value, units[exp])
	}
	return fmt.Sprintf("%.2f %s", value, units[exp])
}

// formatDurationHuman returns a cleaner duration string for display.
func formatDurationHuman(d time.Duration) string {
	if d < 0 {
		return d.String()
	}
	// For very short durations, show with limited precision
	if d < time.Second {
		ms := d.Milliseconds()
		if ms > 0 {
			return fmt.Sprintf("%dms", ms)
		}
		return d.String()
	}
	// For durations under a minute, show seconds with one decimal
	if d < time.Minute {
		secs := d.Seconds()
		if secs == float64(int(secs)) {
			return fmt.Sprintf("%ds", int(secs))
		}
		return fmt.Sprintf("%.1fs", secs)
	}
	// For longer durations, use a cleaner format
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	if hours > 0 {
		if secs > 0 {
			return fmt.Sprintf("%dh %dm %ds", hours, mins, secs)
		}
		if mins > 0 {
			return fmt.Sprintf("%dh %dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	}
	if secs > 0 {
		return fmt.Sprintf("%dm %ds", mins, secs)
	}
	return fmt.Sprintf("%dm", mins)
}

// formatPercent formats a percentage with appropriate precision.
func formatPercent(value float64) string {
	if value == float64(int(value)) {
		return fmt.Sprintf("%.0f%%", value)
	}
	return fmt.Sprintf("%.1f%%", value)
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
