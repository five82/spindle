package encode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/five82/spindle/reel/internal/chunk"
	"github.com/five82/spindle/reel/internal/quality"
	"github.com/five82/spindle/reel/internal/video"
)

func TestTargetQualityWarmupScoresSameProbeInBothMetrics(t *testing.T) {
	path := writeTestY4M(t, 32, 32, 3)
	inf, err := video.Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	for _, name := range []string{"encode", "probes", "tq"} {
		if err := os.MkdirAll(filepath.Join(work, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var lines []string
	tq := TargetQualityConfig{Metric: quality.MetricSSIMU2, Target: 80, Tolerance: 2, CRFMin: 10, CRFMax: 50, MaxProbes: 1, InitialCRF: 30, Verbose: func(s string) { lines = append(lines, s) }}
	limiter := newAdaptiveLimiter(1, 1, 1, 3, nil, nil)
	r := newTargetQualityRun(tq, &EncodeConfig{CRF: 30, Preset: 12}, path, work, inf, nil, 32, 32, limiter, 1, nil, nil)
	cv := &syntheticScorer{t: t}
	ss := &syntheticScorer{t: t}
	r.metricPool = make(chan quality.ChunkScorer, 1)
	r.warmupPool = make(chan quality.ChunkScorer, 1)
	r.metricPool <- ss
	r.warmupPool <- cv
	if _, err := limiter.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := r.processChunk(context.Background(), chunk.Chunk{Idx: 0, End: 3})
	limiter.release()
	if result.Error != nil || result.Frames != 3 || cv.calls != 1 || ss.calls != 1 || r.calibration.SampleCount() != 1 || r.warmupOutstanding.Load() != 0 || len(lines) == 0 {
		t.Fatalf("warmup: %+v, cv=%d ss=%d samples=%d lines=%v", result, cv.calls, ss.calls, r.calibration.SampleCount(), lines)
	}
	if !strings.Contains(strings.Join(lines, " "), "TQ final") {
		t.Fatalf("missing final probe log: %v", lines)
	}
}
