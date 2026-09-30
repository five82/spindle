package logs

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"
)

// Default returns l if non-nil, otherwise slog.Default().
func Default(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}

// FormatDuration renders a duration for log attributes: millisecond
// precision, Go duration syntax ("12m15.231s"), which time.ParseDuration
// reads back.
func FormatDuration(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}

// FormatCounts renders a name->count map as a stable "a=1,b=2" string for
// log attributes; empty maps render as "none".
func FormatCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(counts))
	for name, n := range counts {
		parts = append(parts, fmt.Sprintf("%s=%d", name, n))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// Tail reads the last limit lines from a log file.
func Tail(path string, limit int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	if limit <= 0 {
		limit = 1000
	}

	lines := make([]string, limit)
	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines[count%limit] = scanner.Text()
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	if count < limit {
		return lines[:count], nil
	}
	result := make([]string, limit)
	start := count % limit
	copy(result, lines[start:])
	copy(result[limit-start:], lines[:start])
	return result, nil
}
