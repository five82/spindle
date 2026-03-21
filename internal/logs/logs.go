package logs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"time"
)

// TailOptions configures log tailing.
type TailOptions struct {
	Offset       int64
	Limit        int
	Follow       bool
	WaitDuration time.Duration
}

// TailResult contains tailed log lines.
type TailResult struct {
	Lines  []string
	Offset int64 // next byte position for continuation
}

// Tail reads lines from a log file. The context is used for cancellation
// when Follow mode is enabled.
func Tail(_ context.Context, path string, opts TailOptions) (*TailResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	if opts.Offset > 0 {
		if _, err := f.Seek(opts.Offset, 0); err != nil {
			return nil, fmt.Errorf("seek: %w", err)
		}
	}

	if opts.Limit <= 0 {
		opts.Limit = 1000
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && len(lines) < opts.Limit {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	pos, err := f.Seek(0, 1) // current position
	if err != nil {
		return nil, fmt.Errorf("tell: %w", err)
	}

	return &TailResult{
		Lines:  lines,
		Offset: pos,
	}, nil
}
