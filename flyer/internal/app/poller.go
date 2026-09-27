package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/five82/flyer/internal/spindle"
	"github.com/five82/flyer/internal/state"
)

const (
	defaultPollInterval = 2 * time.Second
	maxBackoff          = 30 * time.Second
)

// StartPoller launches a background goroutine that refreshes the store at a
// fixed cadence with exponential backoff on failures. It returns immediately.
func StartPoller(ctx context.Context, store *state.Store, client *spindle.Client, interval time.Duration) {
	if interval <= 0 {
		interval = defaultPollInterval
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var consecutiveFailures int
		var lastPollTime time.Time

		for {
			// Calculate backoff based on consecutive failures
			backoff := calculateBackoff(consecutiveFailures, interval)

			// Skip poll if within backoff period
			if consecutiveFailures > 0 && time.Since(lastPollTime) < backoff {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					continue
				}
			}

			lastPollTime = time.Now()
			err := refresh(ctx, store, client)
			if err != nil {
				consecutiveFailures++
			} else {
				consecutiveFailures = 0
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// calculateBackoff returns the backoff duration for the given failure count.
// Uses exponential backoff: interval * 2^failures, capped at maxBackoff.
func calculateBackoff(failures int, baseInterval time.Duration) time.Duration {
	if failures <= 0 {
		return baseInterval
	}
	// Exponential backoff: baseInterval * 2^failures
	backoff := baseInterval * time.Duration(1<<failures)
	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

// refresh fetches status and queue concurrently and applies both to the
// store atomically: only when both fetches succeed does the store see new
// data, matching the prior sequential behavior where a failure on either
// endpoint left the store untouched.
func refresh(ctx context.Context, store *state.Store, client *spindle.Client) error {
	var wg sync.WaitGroup
	var status *spindle.StatusResponse
	var queue []spindle.QueueItem
	var statusErr, queueErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		status, statusErr = client.FetchStatus(ctx)
	}()
	go func() {
		defer wg.Done()
		queue, queueErr = client.FetchQueue(ctx)
	}()
	wg.Wait()

	if statusErr != nil || queueErr != nil {
		err := combineFetchErrors(statusErr, queueErr)
		store.Update(nil, nil, err)
		return err
	}

	store.Update(status, queue, nil)
	return nil
}

// combineFetchErrors merges the status and queue fetch errors into a single
// reported failure. When both fail, both messages are included so neither
// failure is silently dropped.
func combineFetchErrors(statusErr, queueErr error) error {
	switch {
	case statusErr != nil && queueErr != nil:
		return fmt.Errorf("status: %w; queue: %v", statusErr, queueErr)
	case statusErr != nil:
		return statusErr
	default:
		return queueErr
	}
}
