package subtitles

import (
	"context"
	"errors"
	"time"

	"spindle/internal/logging"
	"spindle/internal/subtitles/opensubtitles"
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
		if !opensubtitles.IsRetriable(err) || attempt >= opensubtitles.MaxRateRetries {
			return err
		}
		attempt++
		backoff := opensubtitles.InitialBackoff * time.Duration(1<<uint(attempt-1))
		if backoff > opensubtitles.MaxBackoff {
			backoff = opensubtitles.MaxBackoff
		}
		if s.logger != nil {
			s.logger.Warn("opensubtitles rate limited, retrying",
				logging.Duration("backoff", backoff),
				logging.Int("attempt", attempt),
				logging.Int("max_attempts", opensubtitles.MaxRateRetries),
				logging.Error(err),
				logging.String("reason", "rate limit or transient network error"),
				logging.String(logging.FieldEventType, "opensubtitles_rate_limited"),
				logging.String(logging.FieldErrorHint, "wait for rate limits or check network connectivity"),
			)
		}
		if err := opensubtitles.SleepWithContext(ctx, backoff); err != nil {
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
	if elapsed >= opensubtitles.MinInterval {
		return nil
	}
	return opensubtitles.SleepWithContext(ctx, opensubtitles.MinInterval-elapsed)
}

func (s *Service) markOpenSubtitlesCall() {
	s.openSubsMu.Lock()
	s.openSubsLastCall = time.Now()
	s.openSubsMu.Unlock()
}
