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

// Tail reads up to limit lines from a log file.
func Tail(path string, limit int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	if limit <= 0 {
		limit = 1000
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && len(lines) < limit {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return lines, nil
}
