package logs

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
)

// Default returns l if non-nil, otherwise slog.Default().
func Default(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
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
