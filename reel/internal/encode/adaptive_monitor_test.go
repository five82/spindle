package encode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/util"
)

func TestAdaptiveMonitorSamplePreservesSwapAndPressureDecisions(t *testing.T) {
	var warnings []string
	limiter := newAdaptiveLimiter(8, 6, 8, 0, nil, func(s string) { warnings = append(warnings, s) })
	stats := util.MemoryStats{MemTotal: 1000, MemAvailable: 900, SwapTotal: 10 << 30, SwapFree: (10 << 30) - 100}
	canceled := false
	var fatalErr error
	cancel := func() { canceled = true }
	setError := func(err error) { fatalErr = err }
	last := limiter.monitorSample(stats, 100, 100, cancel, setError)
	if last != 100 || fatalErr != nil || canceled {
		t.Fatalf("healthy sample: swap %d, err %v, canceled %v", last, fatalErr, canceled)
	}
	// Growing swap with plenty of available memory does not throttle workers.
	stats.SwapFree = stats.SwapTotal - 400
	last = limiter.monitorSample(stats, 100, last, cancel, setError)
	_, target, _ := limiter.stats()
	if last != 400 || target != 6 || canceled {
		t.Fatalf("healthy swap growth: swap %d, target %d, canceled %v", last, target, canceled)
	}
	stats.MemAvailable = uint64(float64(stats.MemTotal) * memoryPressureAvailableFraction / 2)
	stats.SwapFree = stats.SwapTotal - 410
	last = limiter.monitorSample(stats, 100, last, cancel, setError)
	_, target, _ = limiter.stats()
	if target >= 6 || len(warnings) != 1 || fatalErr != nil {
		t.Fatalf("pressure: target %d, warnings %v, err %v", target, warnings, fatalErr)
	}
	stats.MemAvailable = 0
	limiter.monitorSample(stats, 100, last, cancel, setError)
	if !errors.Is(fatalErr, ErrMemoryPressure) || !canceled || len(warnings) != 2 || !strings.Contains(warnings[1], "canceling") {
		t.Fatalf("critical: err %v, canceled %v, warnings %v", fatalErr, canceled, warnings)
	}
}

func TestAdaptiveMonitorStopsOnAlreadyCanceledContext(t *testing.T) {
	limiter := newAdaptiveLimiter(2, 1, 2, 0, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	limiter.monitor(ctx, cancel, func(error) { t.Fatal("error on canceled monitor") })
}
