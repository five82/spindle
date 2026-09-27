package encode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/worker"
)

func TestTargetRunProgressAndFirstError(t *testing.T) {
	r := &targetQualityRun{limiter: newAdaptiveLimiter(3, 2, 3, 0, nil, nil)}
	r.flightCond = sync.NewCond(&r.flightMu)
	var events []worker.Progress
	r.progressCb = func(p worker.Progress) { events = append(events, p) }
	r.inFlight.Store(2)
	r.progress = worker.Progress{FramesComplete: 42}
	r.progressMu.Lock()
	snapshot := r.snapshotProgressLocked()
	r.progressMu.Unlock()
	if snapshot.InFlight != 2 || snapshot.TargetWorkers != 2 || snapshot.MaxWorkers != 3 || snapshot.FramesComplete != 42 {
		t.Fatalf("snapshot: %+v", snapshot)
	}
	r.emitProgress(snapshot)
	if len(events) != 1 || events[0] != snapshot {
		t.Fatalf("events: %+v", events)
	}
	first := errors.New("first")
	r.setError(first)
	r.setError(errors.New("second"))
	if r.getError() != first {
		t.Fatalf("error = %v", r.getError())
	}
	r.chunkDone()
	if r.inFlight.Load() != 1 {
		t.Fatal("chunk not released")
	}
	r.progressCb = nil
	r.emitProgress(snapshot)
}

func TestTargetRunDispatchAndCollect(t *testing.T) {
	dir := t.TempDir()
	r := newTargetQualityRun(TargetQualityConfig{Metric: quality.MetricCVVDP, MetricWorkers: 1, InitialCRF: 30}, &EncodeConfig{}, "input", dir, testVideoInfo(), nil, 1920, 1080, newAdaptiveLimiter(2, 1, 2, 0, nil, nil), 2, nil, nil)
	if err := os.Mkdir(filepath.Join(dir, "encode"), 0700); err != nil {
		t.Fatal(err)
	}
	// A canceled dispatch must close the channel without acquiring a slot.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := make(chan chunk.Chunk, 1)
	r.dispatch(ctx, []chunk.Chunk{{Idx: 1}}, out)
	if _, open := <-out; open {
		t.Fatal("dispatch channel left open")
	}
	if active, _, _ := r.limiter.stats(); active != 0 {
		t.Fatalf("slots leaked: %d", active)
	}
	// A completed result updates counters and logs; an error preserves the first failure.
	r.inFlight.Store(2)
	results := make(chan targetQualityResult, 2)
	results <- targetQualityResult{EncodeResult: worker.EncodeResult{ChunkIdx: 1, Frames: 24, Size: 123}, Log: chunkTargetLog{ChunkIdx: 1}}
	failure := errors.New("encode failed")
	results <- targetQualityResult{EncodeResult: worker.EncodeResult{ChunkIdx: 2, Error: failure}}
	close(results)
	r.collect(results)
	if r.inFlight.Load() != 0 || r.progress.ChunksComplete != 1 || r.progress.FramesComplete != 24 || r.progress.BytesComplete != 123 || len(r.logs) != 1 || r.getError() != failure {
		t.Fatalf("collect: progress=%+v logs=%v error=%v", r.progress, r.logs, r.getError())
	}
}
