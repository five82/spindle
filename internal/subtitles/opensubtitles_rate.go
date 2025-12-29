package subtitles

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"spindle/internal/logging"
)

func (s *Service) invokeOpenSubtitles(ctx context.Context, op func() error) error {
	if op == nil {
		return errors.New("opensubtitles operation unavailable")
	}
	attempt := 0
	for {
		if err := s.waitForOpenSubtitlesWindow(ctx); err != nil {
			return err
		}
		err := op()
		s.markOpenSubtitlesCall()
		if err == nil {
			return nil
		}
		if !isOpenSubtitlesRetriable(err) || attempt >= openSubtitlesMaxRateRetries {
			return err
		}
		attempt++
		backoff := openSubtitlesInitialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > openSubtitlesMaxBackoff {
			backoff = openSubtitlesMaxBackoff
		}
		if s.logger != nil {
			s.logger.Warn("opensubtitles rate limited, retrying",
				logging.Duration("backoff", backoff),
				logging.Int("attempt", attempt),
				logging.Int("max_attempts", openSubtitlesMaxRateRetries),
				logging.Error(err),
				logging.String("reason", "rate limit or transient network error"),
			)
		}
		if err := sleepWithContext(ctx, backoff); err != nil {
			return err
		}
	}
}

func (s *Service) waitForOpenSubtitlesWindow(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context unavailable")
	}
	s.openSubsMu.Lock()
	lastCall := s.openSubsLastCall
	s.openSubsMu.Unlock()
	if lastCall.IsZero() {
		return nil
	}
	elapsed := time.Since(lastCall)
	if elapsed >= openSubtitlesMinInterval {
		return nil
	}
	return sleepWithContext(ctx, openSubtitlesMinInterval-elapsed)
}

func (s *Service) markOpenSubtitlesCall() {
	s.openSubsMu.Lock()
	s.openSubsLastCall = time.Now()
	s.openSubsMu.Unlock()
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
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

func isOpenSubtitlesRetriable(err error) bool {
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
