package encode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/quality"
)

func TestTargetRunClosesBothScorerPoolsOnce(t *testing.T) {
	logger, logs := captureLogger()
	r := newSSIMU2TestRun(t, logger)
	for _, pool := range []chan quality.ChunkScorer{r.metricPool, r.warmupPool} {
		for range cap(pool) {
			pool <- &syntheticScorer{}
		}
	}
	r.closeScorerPools()
	r.closeWarmupPool()
	if strings.Count(logs.String(), "scorers closed") != 1 {
		t.Fatalf("warmup close: %s", logs.String())
	}
	// A nil pool and an empty run require no GPU resources.
	closeScorerPool(nil)
	(&targetQualityRun{}).closeWarmupPool()
}

func TestTargetRunWorkerCancellationAndPriorError(t *testing.T) {
	r := newSSIMU2TestRun(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := r.limiter.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	jobs := make(chan chunk.Chunk, 1)
	jobs <- chunk.Chunk{Idx: 7}
	close(jobs)
	results := make(chan targetQualityResult, 1)
	r.runWorker(ctx, jobs, results)
	res := <-results
	if res.ChunkIdx != 7 || !errors.Is(res.Error, context.Canceled) {
		t.Fatalf("canceled worker = %+v", res)
	}
	if active, _, _ := r.limiter.stats(); active != 0 {
		t.Fatalf("active slots = %d", active)
	}
	r.setError(errors.New("first failure"))
	if _, err := r.limiter.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	jobs = make(chan chunk.Chunk, 1)
	jobs <- chunk.Chunk{Idx: 8}
	close(jobs)
	r.runWorker(context.Background(), jobs, results)
	if len(results) != 0 {
		t.Fatal("worker ran after error")
	}
}

func TestTargetRunDispatchFeedsChunksAndHonorsPriorFailure(t *testing.T) {
	r := newTargetQualityRun(TargetQualityConfig{Metric: quality.MetricCVVDP, MetricWorkers: 1, InitialCRF: 30}, &EncodeConfig{}, "input", t.TempDir(), testVideoInfo(), nil, 1920, 1080, newAdaptiveLimiter(2, 2, 2, 0, nil), 2, nil, nil)
	jobs := make(chan chunk.Chunk, 2)
	r.dispatch(context.Background(), []chunk.Chunk{{Idx: 1}, {Idx: 2}}, jobs)
	if a, b := (<-jobs).Idx, (<-jobs).Idx; a != 1 || b != 2 {
		t.Fatalf("dispatch order: %d, %d", a, b)
	}
	if _, open := <-jobs; open {
		t.Fatal("jobs not closed")
	}
	if r.inFlight.Load() != 2 {
		t.Fatalf("in flight = %d", r.inFlight.Load())
	}
	for range 2 {
		r.limiter.release()
		r.chunkDone()
	}
	r.setError(errors.New("stop"))
	jobs = make(chan chunk.Chunk, 1)
	r.dispatch(context.Background(), []chunk.Chunk{{Idx: 3}}, jobs)
	if _, open := <-jobs; open {
		t.Fatal("dispatch ignored error")
	}
}

func TestTargetRunWithSlotReleasedPreservesCallbackError(t *testing.T) {
	r := &targetQualityRun{limiter: newAdaptiveLimiter(1, 1, 1, 0, nil)}
	if _, err := r.limiter.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("callback")
	if err := r.withSlotReleased(context.Background(), func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("lost error: %v", err)
	}
	r.limiter.release()
	// Keep the progress type checked even with a nil callback.
	r.emitProgress()
}
