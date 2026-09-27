package encode

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
	"github.com/five82/spindle/reel/internal/worker"
)

func TestTargetQualityRunWithSyntheticScorers(t *testing.T) {
	path := writeTestY4M(t, 32, 32, 6)
	inf, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	chunks := []chunk.Chunk{{Idx: 0, Start: 0, End: 3}, {Idx: 1, Start: 3, End: 6}}
	var created atomic.Int32
	var events []worker.Progress
	tq := TargetQualityConfig{
		Metric: quality.MetricCVVDP, Target: 9.5, Tolerance: 0.1, CRFMin: 10, CRFMax: 50,
		MaxProbes: 1, InitialCRF: 30, MetricWorkers: 1,
		scorerFactory: func(kind quality.MetricKind, width, height uint32, _ *video.Info, _ string) (quality.ChunkScorer, error) {
			if kind != quality.MetricCVVDP || width != 32 || height != 32 {
				t.Errorf("scorer requested for %s %dx%d", kind, width, height)
			}
			created.Add(1)
			return &syntheticScorer{t: t}, nil
		},
	}
	workers, stats, err := EncodeTargetQuality(context.Background(), chunks, path, inf, &EncodeConfig{CRF: 30, Preset: 12}, work, nil, func(p worker.Progress) { events = append(events, p) }, tq)
	if err != nil {
		t.Fatal(err)
	}
	if workers < 1 || stats == nil || created.Load() != 1 {
		t.Fatalf("run = workers %d stats %+v scorers %d", workers, stats, created.Load())
	}
	if len(events) != 2 || events[len(events)-1].ChunksComplete != 2 || events[len(events)-1].FramesComplete != 6 {
		t.Fatalf("progress = %+v", events)
	}
	for _, ch := range chunks {
		if _, err := os.Stat(chunk.IVFPath(work, ch.Idx)); err != nil {
			t.Fatalf("encoded chunk %d: %v", ch.Idx, err)
		}
	}
	// The completed chunks must be found on resume without reopening GPU handlers.
	tq.scorerFactory = func(quality.MetricKind, uint32, uint32, *video.Info, string) (quality.ChunkScorer, error) {
		t.Fatal("opened scorer for completed run")
		return nil, nil
	}
	_, stats, err = EncodeTargetQuality(context.Background(), chunks, path, inf, &EncodeConfig{CRF: 30, Preset: 12}, work, nil, nil, tq)
	if err != nil || stats != nil {
		t.Fatalf("resume = %+v, %v", stats, err)
	}
}

func TestTargetQualityPoolFactoryFailuresReleaseCreatedScorers(t *testing.T) {
	sentinel := errors.New("scorer init failed")
	created := 0
	closed := 0
	factory := func(quality.MetricKind, uint32, uint32, *video.Info, string) (quality.ChunkScorer, error) {
		created++
		if created == 2 {
			return nil, sentinel
		}
		return &closeTrackingScorer{closed: &closed}, nil
	}
	pool, err := newScorerPoolWithFactory(quality.MetricCVVDP, 2, 32, 32, &video.Info{}, "", factory)
	if pool != nil || !errors.Is(err, sentinel) || closed != 1 {
		t.Fatalf("pool = %v, err %v, closed %d", pool, err, closed)
	}
	// A pool failure during a real run must stop before dispatching any chunks.
	created = 0
	tq := TargetQualityConfig{Metric: quality.MetricCVVDP, MetricWorkers: 2, scorerFactory: factory}
	_, _, err = EncodeTargetQuality(context.Background(), []chunk.Chunk{{Idx: 0, End: 3}}, "unused", &video.Info{Width: 32, Height: 32, FPSNum: 25, FPSDen: 1}, &EncodeConfig{}, t.TempDir(), nil, nil, tq)
	if !errors.Is(err, sentinel) || closed != 2 {
		t.Fatalf("run = %v, released %d scorers", err, closed)
	}
}

type closeTrackingScorer struct{ closed *int }

func (*closeTrackingScorer) ScoreChunk(context.Context, quality.ChunkScoreRequest) (float32, float64, error) {
	return 0, 0, nil
}
func (s *closeTrackingScorer) Close() error { *s.closed++; return nil }
