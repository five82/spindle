package opensubtitles

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// Rate limiting configuration for OpenSubtitles API calls.
const (
	MinInterval    = time.Second
	MaxRateRetries = 6
	InitialBackoff = 2 * time.Second
	MaxBackoff     = 60 * time.Second
)

// SleepWithContext blocks for the given duration, returning early if the
// context is cancelled.
func SleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// IsRetriable reports whether err represents a transient condition that
// warrants an automatic retry (rate limits, timeouts, connection errors).
func IsRetriable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "429") || strings.Contains(message, "rate limit") {
		return true
	}
	// Server errors are typically transient (outages, deploys, overload).
	for _, code := range []string{"502", "503", "504"} {
		if strings.Contains(message, code) {
			return true
		}
	}
	timeoutTokens := []string{
		"timeout",
		"deadline exceeded",
		"client.timeout exceeded",
		"connection reset",
		"connection refused",
		"temporary failure",
		"awaiting headers",
	}
	for _, token := range timeoutTokens {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}
